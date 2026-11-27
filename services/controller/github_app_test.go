package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type githubTestTransport func(*http.Request) (*http.Response, error)

func (f githubTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestGitHubAppTokenContract(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	binding := githubBinding{"00000000-0000-4000-8000-000000000001", 123, 456, "owner/repo"}
	permissions := `"permissions":{"actions":"read","checks":"read","contents":"read","pull_requests":"read","metadata":"read"}`
	success := `{"token":"synthetic-only-token","expires_at":"2026-11-27T01:00:00Z","repositories":[{"id":456}],` + permissions + `}`
	response := success
	status := 201
	calls := 0
	transport := githubTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.String() != "https://api.github.com/app/installations/123/access_tokens" || r.Method != http.MethodPost || r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
			t.Fatal("unexpected endpoint or method")
		}
		parts := strings.Split(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), ".")
		if len(parts) != 3 {
			t.Fatal("invalid JWT")
		}
		enc := base64.RawURLEncoding
		header, _ := enc.DecodeString(parts[0])
		claims, _ := enc.DecodeString(parts[1])
		sig, _ := enc.DecodeString(parts[2])
		hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		var payload struct {
			Issuer string `json:"iss"`
			IAT    int64  `json:"iat"`
			EXP    int64  `json:"exp"`
		}
		if string(header) != `{"alg":"RS256","typ":"JWT"}` || json.Unmarshal(claims, &payload) != nil || payload.Issuer != "mock-app" || payload.IAT != now.Add(-time.Minute).Unix() || payload.EXP != now.Add(5*time.Minute).Unix() || rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, hash[:], sig) != nil {
			t.Fatal("invalid signed claims")
		}
		var req struct {
			Repositories []int64           `json:"repository_ids"`
			Permissions  map[string]string `json:"permissions"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || len(req.Repositories) != 1 || req.Repositories[0] != 456 || len(req.Permissions) != 5 {
			t.Fatal("unscoped token request")
		}
		for _, name := range []string{"actions", "checks", "contents", "pull_requests", "metadata"} {
			if req.Permissions[name] != "read" {
				t.Fatal("unexpected permission")
			}
		}
		return &http.Response{StatusCode: status, Header: http.Header{"Location": {"https://example.invalid/redirect"}}, Body: io.NopCloser(strings.NewReader(response)), Request: r}, nil
	})
	if _, err := newGitHubApp("bad issuer", key, transport); err == nil {
		t.Fatal("invalid issuer accepted")
	}
	a, err := newGitHubApp("mock-app", key, transport)
	if err != nil {
		t.Fatal(err)
	}
	token, err := a.mint(context.Background(), binding, now)
	if err != nil || token.value != "synthetic-only-token" || !token.expiresAt.Equal(now.Add(time.Hour)) {
		t.Fatal("valid token response rejected", err)
	}
	for _, bad := range []string{
		"not-json",
		strings.Replace(success, `"id":456`, `"id":457`, 1),
		strings.Replace(success, `{"id":456}`, `{"id":456},{"id":457}`, 1),
		strings.Replace(success, `"actions":"read"`, `"actions":"write"`, 1),
		strings.Replace(success, `"metadata":"read"`, `"administration":"read"`, 1),
		strings.Replace(success, "2026-11-27T01:00:00Z", "2026-11-27T00:00:00Z", 1),
		strings.Replace(success, "2026-11-27T01:00:00Z", "2026-11-27T03:00:00Z", 1),
		strings.Replace(success, "synthetic-only-token", "token with space", 1),
		strings.Repeat("x", 256*1024+1),
	} {
		response = bad
		if _, err := a.mint(context.Background(), binding, now); !errors.Is(err, errGitHubAuthorization) {
			t.Fatal("invalid token response accepted")
		}
	}
	response = success
	for _, code := range []int{302, 401, 403, 429, 500} {
		status = code
		before := calls
		_, err := a.mint(context.Background(), binding, now)
		if calls != before+1 {
			t.Fatal("unexpected retry", code)
		}
		if code == 429 || code == 500 {
			var retry githubReadError
			if !errors.As(err, &retry) || !retry.retryAt.Equal(now.Add(time.Minute)) {
				t.Fatal("missing retry deadline", code, err)
			}
		} else if !errors.Is(err, errGitHubAuthorization) {
			t.Fatal("redirect or non-success accepted", code)
		}
	}
	if _, err := a.mint(context.Background(), githubBinding{}, now); !errors.Is(err, errGitHubAuthorization) {
		t.Fatal("invalid binding accepted")
	}
}

func TestPostgresGitHubBinding(t *testing.T) {
	ownerDSN, appDSN := os.Getenv("BROWSEAI_TEST_OWNER_DSN"), os.Getenv("BROWSEAI_TEST_APP_DSN")
	if ownerDSN == "" && appDSN == "" {
		t.Skip("isolated PostgreSQL integration only")
	}
	if ownerDSN == "" || appDSN == "" {
		t.Fatal("incomplete test configuration")
	}
	open := func(dsn string) *sql.DB {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	owner, db := open(ownerDSN), open(appDSN)
	ctx := context.Background()
	p := principal{"00000000-0000-4000-8000-000000000031", "00000000-0000-4000-8000-000000000001", roleUser}
	other := principal{"00000000-0000-4000-8000-000000000032", "00000000-0000-4000-8000-000000000002", roleUser}
	projects := projectStore{db}
	create := func(p principal, name string) projectRecord {
		r, err := projects.create(ctx, p, projectCreate{"GitHub binding", 456, name, "feat/binding"})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	pa, pb, mismatch := create(p, "owner/repo"), create(other, "owner/repo"), create(p, "owner/other")
	s := githubBindingStore{db}
	reject := func(p principal, id string) {
		t.Helper()
		if _, err := s.forProject(ctx, p, id); !errors.Is(err, errResourceMissing) {
			t.Fatal("unverified binding accepted", err)
		}
	}
	reject(p, pa.ID)
	if _, err := owner.Exec("INSERT INTO browseai.github_installations(installation_id,tenant_id) VALUES(123,$1)", p.tenantID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec("INSERT INTO browseai.github_repositories(tenant_id,installation_id,repository_id,full_name) VALUES($1,123,456,'owner/repo')", p.tenantID); err != nil {
		t.Fatal(err)
	}
	b, err := s.forProject(ctx, p, pa.ID)
	if err != nil || b.installationID != 123 || b.repositoryID != 456 || b.tenantID != p.tenantID {
		t.Fatal("binding missing", err)
	}
	reject(other, pa.ID)
	reject(other, pb.ID)
	reject(p, mismatch.ID)
	_, err = tenantTx(ctx, db, other, func(tx *sql.Tx) (bool, error) {
		_, err := tx.Exec("INSERT INTO browseai.github_installations(installation_id,tenant_id) VALUES(999,$1)", other.tenantID)
		return false, err
	})
	if err == nil {
		t.Fatal("runtime allowed self-binding")
	}
	if _, err := owner.Exec("UPDATE browseai.github_installations SET enabled=false WHERE installation_id=123"); err != nil {
		t.Fatal(err)
	}
	reject(p, pa.ID)
}
