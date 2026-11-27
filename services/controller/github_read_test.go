package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGitHubBranchObservation(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	binding := githubBinding{"00000000-0000-4000-8000-000000000001", 123, 456, "owner/repo"}
	const sha = "0123456789abcdef0123456789abcdef01234567"
	const tokenBody = `{"token":"synthetic-only-token","expires_at":"2026-11-27T01:00:00Z","repositories":[{"id":456}],"permissions":{"actions":"read","checks":"read","contents":"read","pull_requests":"read","metadata":"read"}}`
	const goodRepo = `{"id":456,"full_name":"owner/repo","archived":false,"disabled":false}`
	const goodRef = `{"name":"feat/demo","commit":{"sha":"` + sha + `"}}`
	repoBody, refBody := goodRepo, goodRef
	refStatus := http.StatusOK
	refHeaders := http.Header{}
	calls := 0
	a, err := newGitHubApp("mock-app", key, githubTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var body string
		code := http.StatusOK
		headers := http.Header{}
		if r.URL.Host != "api.github.com" || r.URL.Scheme != "https" {
			t.Fatal("unexpected destination")
		}
		switch r.URL.EscapedPath() {
		case "/app/installations/123/access_tokens":
			body, code = tokenBody, http.StatusCreated
			if r.Method != "POST" {
				t.Fatal("unexpected token method")
			}
		case "/repos/owner/repo":
			body = repoBody
		case "/repos/owner/repo/branches/feat%2Fdemo":
			body, code, headers = refBody, refStatus, refHeaders
		default:
			t.Fatal("unexpected path", r.URL.EscapedPath())
		}
		if r.Method == "GET" && r.Header.Get("Authorization") != "Bearer synthetic-only-token" {
			t.Fatal("wrong credential scope")
		}
		return &http.Response{StatusCode: code, Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	result, err := a.observeBranch(ctx, binding, "feat/demo", now)
	if err != nil || result.headSHA != sha || result.target != (verifiedBranch{456, "feat/demo"}) || calls != 3 {
		t.Fatal("valid observation rejected", result, err)
	}
	for _, bad := range []string{
		strings.Replace(goodRepo, "456", "457", 1),
		strings.Replace(goodRepo, "owner/repo", "other/repo", 1),
		strings.Replace(goodRepo, `"archived":false`, `"archived":true`, 1),
		strings.Replace(goodRepo, `"disabled":false`, `"disabled":true`, 1),
		"invalid-json",
	} {
		repoBody = bad
		before := calls
		if _, err := a.observeBranch(ctx, binding, "feat/demo", now); err == nil || calls != before+2 {
			t.Fatal("invalid repository accepted")
		}
	}
	repoBody = goodRepo
	for _, bad := range []string{
		strings.Replace(goodRef, "feat/demo", "feat/other", 1),
		strings.Replace(goodRef, sha, "not-sha", 1),
		strings.Repeat("x", 2*1024*1024+1),
	} {
		refBody = bad
		if _, err := a.observeBranch(ctx, binding, "feat/demo", now); err == nil {
			t.Fatal("invalid branch accepted")
		}
	}
	refBody = goodRef
	for _, status := range []int{401, 403, 404, 302, 500, 429} {
		refStatus = status
		if _, err := a.observeBranch(ctx, binding, "feat/demo", now); err == nil {
			t.Fatal("failed read accepted", status)
		}
	}
	refStatus = 429
	refHeaders = http.Header{"Retry-After": {"120"}, "X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"1795737900"}}
	_, err = a.observeBranch(ctx, binding, "feat/demo", now)
	var delayed githubReadError
	if !errors.As(err, &delayed) || delayed.retryAt.Before(now.Add(120*time.Second)) {
		t.Fatal("rate limit lost", err)
	}
	before := calls
	if _, err := a.observeBranch(ctx, binding, "../wrong", now); err == nil || calls != before {
		t.Fatal("invalid branch caused request")
	}
	var dst any
	if err := a.readJSON(ctx, githubToken{"synthetic-only-token", now}, "/repos/owner/repo", nil, now, &dst); !errors.Is(err, errGitHubAuthorization) {
		t.Fatal("expired credential accepted")
	}
	if err := a.readJSON(ctx, githubToken{"synthetic-only-token", now.Add(time.Hour)}, "https://other.example/", url.Values{}, now, &dst); !errors.Is(err, errGitHubAuthorization) {
		t.Fatal("external URL accepted")
	}
}

func TestGitHubRetryAt(t *testing.T) {
	now := time.Unix(1000, 0).UTC()
	for _, item := range []struct {
		headers http.Header
		want    time.Time
	}{
		{http.Header{}, now.Add(time.Minute)},
		{http.Header{"Retry-After": {"120"}}, now.Add(120 * time.Second)},
		{http.Header{"Retry-After": {now.Add(180 * time.Second).Format(http.TimeFormat)}}, now.Add(180 * time.Second)},
		{http.Header{"Retry-After": {"bad"}, "X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"1300"}}, time.Unix(1301, 0)},
	} {
		if got := githubRetryAt(item.headers, now); !got.Equal(item.want) {
			t.Fatal("retry deadline mismatch", got, item.want)
		}
	}
}
