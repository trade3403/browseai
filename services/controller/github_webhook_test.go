package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubWebhookVerification(t *testing.T) {
	secret := []byte(strings.Repeat("s", 32))
	body := `{"installation":{"id":123},"repository":{"id":456},"action":"completed"}`
	request := func(payload string) *http.Request {
		r := httptest.NewRequest("POST", "/webhook", strings.NewReader(payload))
		mac := hmac.New(sha256.New, secret)
		mac.Write([]byte(payload))
		r.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
		r.Header.Set("X-GitHub-Delivery", "12345678-1234-1234-1234-123456789abc")
		r.Header.Set("X-GitHub-Event", "workflow_run")
		return r
	}
	d, err := verifyGitHubWebhook(request(body), secret)
	if err != nil || d.installationID != 123 || d.repositoryID != 456 || d.bodyDigest != sha256.Sum256([]byte(body)) {
		t.Fatal("valid delivery rejected", err)
	}
	for _, mutate := range []func(*http.Request){
		func(r *http.Request) { r.Header.Del("X-Hub-Signature-256") },
		func(r *http.Request) { r.Header.Set("X-Hub-Signature-256", "sha256="+strings.Repeat("0", 64)) },
		func(r *http.Request) { r.Header.Set("X-GitHub-Delivery", "bad") },
		func(r *http.Request) { r.Header.Add("X-GitHub-Delivery", "duplicate") },
		func(r *http.Request) { r.Header.Set("X-GitHub-Event", "unknown") },
		func(r *http.Request) { r.Method = "GET" },
	} {
		r := request(body)
		mutate(r)
		if _, err := verifyGitHubWebhook(r, secret); err == nil {
			t.Fatal("invalid delivery accepted")
		}
	}
	for _, bad := range []string{"{}", "invalid", `{"installation":{"id":0},"repository":{"id":456}}`, strings.Repeat("x", githubWebhookMaxBody+1)} {
		if _, err := verifyGitHubWebhook(request(bad), secret); err == nil {
			t.Fatal("invalid payload accepted")
		}
	}
	if _, err := verifyGitHubWebhook(request(body), []byte("weak")); err == nil {
		t.Fatal("weak secret accepted")
	}
}
