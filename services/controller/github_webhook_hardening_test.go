package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGitHubWebhookSecretFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "webhook-secret")
	valid := strings.Repeat("s", 32)
	write := func(value string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(valid)
	if runtime.GOOS == "windows" {
		if _, err := loadGitHubWebhookSecret(path); err == nil {
			t.Fatal("unverified Windows ACL accepted")
		}
		return
	}
	got, err := loadGitHubWebhookSecret(path)
	if err != nil || string(got) != valid {
		t.Fatal("valid secret rejected", err)
	}
	for _, bad := range []string{"", strings.Repeat("s", 31), valid + "\n", valid + " ", valid + "\x00", strings.Repeat("s", 4097)} {
		write(bad)
		if _, err := loadGitHubWebhookSecret(path); err == nil {
			t.Fatal("invalid secret accepted")
		}
	}
	write(valid)
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadGitHubWebhookSecret(path); err == nil {
		t.Fatal("public secret accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(path), "link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{link, "relative", path + ".missing"} {
		if _, err := loadGitHubWebhookSecret(bad); err == nil {
			t.Fatal("unsafe secret path accepted")
		}
	}
}

func TestGitHubWebhookConcurrencyRecovery(t *testing.T) {
	key := []byte(strings.Repeat("s", 32))
	body := `{"installation":{"id":123},"repository":{"id":456}}`
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var calls atomic.Int32
	handler := newGitHubWebhookHandler(key, githubRecorderFunc(func(ctx context.Context, d githubWebhookDelivery) (string, error) {
		n := calls.Add(1)
		if n <= 8 {
			entered <- struct{}{}
			<-release
			return "", errGitHubUnavailable
		}
		return "accepted", nil
	}))
	results := make(chan int, 8)
	for i := 0; i < 8; i++ {
		go func() {
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, signedWebhookRequest(key, body))
			results <- w.Code
		}()
	}
	for i := 0; i < 8; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("request did not enter recorder")
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, signedWebhookRequest(key, body))
	if w.Code != 503 || w.Header().Get("Retry-After") != "5" || calls.Load() != 8 {
		t.Fatal("concurrency bound not enforced")
	}
	releaseOnce.Do(func() { close(release) })
	for i := 0; i < 8; i++ {
		select {
		case code := <-results:
			if code != 503 {
				t.Fatal("database outage not propagated", code)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("request slot not released")
		}
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, signedWebhookRequest(key, body))
	if w.Code != 202 || calls.Load() != 9 {
		t.Fatal("receiver failed to recover")
	}
}

func TestGitHubWebhookUnknownLengthAndCancellation(t *testing.T) {
	key := []byte(strings.Repeat("s", 32))
	body := `{"installation":{"id":123},"repository":{"id":456}}`
	var calls int
	handler := newGitHubWebhookHandler(key, githubRecorderFunc(func(ctx context.Context, d githubWebhookDelivery) (string, error) {
		calls++
		if ctx.Err() != context.Canceled {
			t.Fatal("request cancellation lost")
		}
		return "", ctx.Err()
	}))
	r := signedWebhookRequest(key, strings.Repeat("x", githubWebhookMaxBody+1))
	r.ContentLength = -1
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 || calls != 0 {
		t.Fatal("oversized chunked delivery reached storage")
	}
	r = signedWebhookRequest(key, body)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, r.WithContext(ctx))
	if w.Code != 503 || calls != 1 {
		t.Fatal("cancellation not propagated")
	}
	// The handler owns an independent copy of the secret.
	handler = newGitHubWebhookHandler(key, githubRecorderFunc(func(context.Context, githubWebhookDelivery) (string, error) {
		return "accepted", nil
	}))
	original := bytes.Clone(key)
	key[0] = 'x'
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, signedWebhookRequest(original, body))
	if w.Code != http.StatusAccepted {
		t.Fatal("secret alias changed handler")
	}
}
