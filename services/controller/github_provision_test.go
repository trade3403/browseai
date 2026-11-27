package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPostgresGitHubProvisioning(t *testing.T) {
	ownerDSN, appDSN := os.Getenv("BROWSEAI_TEST_OWNER_DSN"), os.Getenv("BROWSEAI_TEST_APP_DSN")
	if ownerDSN == "" && appDSN == "" {
		t.Skip("isolated PostgreSQL integration only")
	}
	if ownerDSN == "" || appDSN == "" {
		t.Fatal("incomplete test configuration")
	}
	open := func(dsn string) *sql.DB {
		t.Helper()
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	owner, runtimeDB := open(ownerDSN), open(appDSN)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()
	installation := "8123"
	valid := true
	a, err := newGitHubApp("mock-app", key, githubTestTransport(func(r *http.Request) (*http.Response, error) {
		var body string
		status := 200
		switch r.URL.Path {
		case "/app":
			body = `{"id":12}`
		case "/app/installations/" + installation:
			body = `{"id":` + installation + `,"app_id":12,"account":{"id":34},"target_id":34,"suspended_at":null}`
		case "/app/installations/" + installation + "/access_tokens":
			status = 201
			body = `{"token":"synthetic","expires_at":"2026-11-27T01:00:00Z","repositories":[{"id":8456}],"permissions":{"actions":"read","checks":"read","contents":"read","pull_requests":"read","metadata":"read"}}`
		case "/repos/owner/provision":
			body = `{"id":8456,"full_name":"owner/provision"}`
			if !valid {
				body = `{"id":999,"full_name":"owner/provision"}`
			}
		default:
			t.Fatal("unexpected request")
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	approval := githubInstallationApproval{
		binding: githubBinding{"00000000-0000-4000-8000-000000000001", 8123, 8456, "owner/provision"},
		appID:   12, accountID: 34,
	}
	s := githubProvisioner{owner, a}
	reject := func(s githubProvisioner, approval githubInstallationApproval) {
		t.Helper()
		if err := s.provision(ctx, approval, now); err == nil {
			t.Fatal("unexpected provisioning success")
		}
	}
	count := func(id int64, want int) {
		t.Helper()
		var n int
		if err := owner.QueryRow("SELECT count(*) FROM browseai.github_installations WHERE installation_id=$1", id).Scan(&n); err != nil || n != want {
			t.Fatal("unexpected persisted installation", n, err)
		}
	}
	valid = false
	reject(s, approval)
	count(8123, 0)
	valid = true
	reject(githubProvisioner{runtimeDB, a}, approval)
	count(8123, 0)
	for i := 0; i < 2; i++ {
		if err := s.provision(ctx, approval, now); err != nil {
			t.Fatal("trusted provisioning failed", err)
		}
	}
	count(8123, 1)
	other := approval
	other.binding.tenantID = "00000000-0000-4000-8000-000000000002"
	reject(s, other)
	installation = "8124"
	conflict := approval
	conflict.binding.installationID = 8124
	reject(s, conflict)
	count(8124, 0)
	installation = "8123"
	if _, err := owner.Exec("UPDATE browseai.github_installations SET enabled=false WHERE installation_id=8123"); err != nil {
		t.Fatal(err)
	}
	reject(s, approval)
}
