package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGitHubInstallationVerification(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	approval := githubInstallationApproval{
		binding: githubBinding{"00000000-0000-4000-8000-000000000001", 123, 456, "owner/repo"},
		appID:   12, accountID: 34,
	}
	installation := `{"id":123,"app_id":12,"account":{"id":34},"target_id":34,"suspended_at":null}`
	response := installation
	a, err := newGitHubApp("mock-app", key, githubTestTransport(func(r *http.Request) (*http.Response, error) {
		body := ""
		status := 200
		switch r.URL.Path {
		case "/app":
			body = `{"id":12}`
		case "/app/installations/123":
			body = response
		case "/app/installations/123/access_tokens":
			status = 201
			body = `{"token":"synthetic","expires_at":"2026-11-27T01:00:00Z","repositories":[{"id":456}],"permissions":{"actions":"read","checks":"read","contents":"read","pull_requests":"read","metadata":"read"}}`
		case "/repos/owner/repo":
			body = `{"id":456,"full_name":"owner/repo"}`
		default:
			t.Fatal("unexpected request")
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.verifyInstallation(context.Background(), approval, now); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(installation, `"app_id":12`, `"app_id":13`, 1),
		strings.Replace(installation, `"account":{"id":34}`, `"account":{"id":35}`, 1),
		strings.Replace(installation, `"target_id":34`, `"target_id":35`, 1),
		strings.Replace(installation, `"suspended_at":null`, `"suspended_at":"2026-11-26T00:00:00Z"`, 1),
	} {
		response = bad
		if err := a.verifyInstallation(context.Background(), approval, now); err == nil {
			t.Fatal("unverified installation accepted")
		}
	}
}
