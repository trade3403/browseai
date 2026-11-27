package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type githubRecorderFunc func(context.Context, githubWebhookDelivery) (string, error)

func (f githubRecorderFunc) record(ctx context.Context, d githubWebhookDelivery) (string, error) {
	return f(ctx, d)
}

func signedWebhookRequest(secret []byte, body string) *http.Request {
	r := httptest.NewRequest("POST", "/webhook/github", strings.NewReader(body))
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(body))
	r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	r.Header.Set("X-GitHub-Delivery", "12345678-1234-1234-1234-123456789abc")
	r.Header.Set("X-GitHub-Event", "workflow_run")
	return r
}

func TestGitHubWebhookHTTP(t *testing.T) {
	key := []byte(strings.Repeat("s", 32))
	body := `{"installation":{"id":123},"repository":{"id":456}}`
	calls := 0
	result := "accepted"
	var failure error
	store := githubRecorderFunc(func(ctx context.Context, d githubWebhookDelivery) (string, error) {
		calls++
		if d.repositoryID != 456 || d.installationID != 123 {
			t.Fatal("wrong verified identity")
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing persistence deadline")
		}
		return result, failure
	})
	server, err := newGitHubWebhookServer("127.0.0.1:8081", key, store)
	if err != nil {
		t.Fatal(err)
	}
	check := func(r *http.Request, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		server.Handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatal("unexpected status", w.Code, want)
		}
	}
	for _, state := range []string{"accepted", "duplicate", "rejected"} {
		result = state
		check(signedWebhookRequest(key, body), 202)
	}
	failure = errGitHubUnavailable
	check(signedWebhookRequest(key, body), 503)
	bad := signedWebhookRequest(key, body)
	bad.Header.Del("X-Hub-Signature-256")
	check(bad, 403)
	check(httptest.NewRequest("GET", "/webhook/github", nil), 405)
	check(httptest.NewRequest("GET", "/api/projects", nil), 404)
	check(signedWebhookRequest(key, strings.Repeat("x", githubWebhookMaxBody+1)), 413)
	if calls != 4 {
		t.Fatal("unverified request reached database", calls)
	}
	if _, err := newGitHubWebhookServer("0.0.0.0:8081", key, store); err == nil {
		t.Fatal("public listener accepted")
	}
}
