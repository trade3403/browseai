package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGitHubActionsListing(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	binding := githubBinding{"00000000-0000-4000-8000-000000000001", 123, 456, "owner/repo"}
	const sha = "0123456789abcdef0123456789abcdef01234567"
	run := `{"id":81,"workflow_id":2,"run_attempt":1,"head_sha":"` + sha + `","head_branch":"feat/demo","event":"push","status":"completed","conclusion":"success","repository":{"id":456},"head_repository":{"id":456}}`
	runBody := `{"total_count":1,"workflow_runs":[` + run + `]}`
	workflowBody := `{"total_count":1,"workflows":[{"id":2,"name":"CI","path":".github/workflows/ci.yml","state":"active"}]}`
	a, err := newGitHubApp("mock-app", key, githubTestTransport(func(r *http.Request) (*http.Response, error) {
		var body string
		status := 200
		switch r.URL.Path {
		case "/app/installations/123/access_tokens":
			status = 201
			body = `{"token":"synthetic","expires_at":"2026-11-27T01:00:00Z","repositories":[{"id":456}],"permissions":{"actions":"read","checks":"read","contents":"read","pull_requests":"read","metadata":"read"}}`
		case "/repos/owner/repo":
			body = `{"id":456,"full_name":"owner/repo"}`
		case "/repos/owner/repo/actions/workflows":
			body = workflowBody
		case "/repos/owner/repo/actions/runs":
			q := r.URL.Query()
			if q.Get("branch") != "feat/demo" || q.Get("head_sha") != sha || q.Get("per_page") != "30" || q.Get("page") != "1" {
				t.Fatal("unexpected run query")
			}
			body = runBody
		default:
			t.Fatal("unexpected endpoint", r.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	w, runs, err := a.listActions(ctx, binding, "feat/demo", sha, now)
	if err != nil || len(w) != 1 || len(runs) != 1 || runs[0].ID != 81 {
		t.Fatal("valid listing rejected", err)
	}
	for _, bad := range []string{
		`{"total_count":301,"workflow_runs":[]}`,
		`{"total_count":1,"workflow_runs":[]}`,
		`{"workflow_runs":[]}`,
		`{"total_count":2,"workflow_runs":[` + run + "," + run + `]}`,
		`{"total_count":1,"workflow_runs":[` + strings.Replace(run, sha, strings.Repeat("a", 40), 1) + `]}`,
		`{"total_count":1,"workflow_runs":[` + strings.Replace(run, `"head_repository":{"id":456}`, `"head_repository":{"id":999}`, 1) + `]}`,
	} {
		runBody = bad
		if _, _, err := a.listActions(ctx, binding, "feat/demo", sha, now); err == nil {
			t.Fatal("invalid listing accepted")
		}
	}
	runBody = `{"total_count":0,"workflow_runs":[]}`
	if _, runs, err := a.listActions(ctx, binding, "feat/demo", sha, now); err != nil || len(runs) != 0 {
		t.Fatal("empty listing rejected", err)
	}
}

func TestGitHubActionsPagination(t *testing.T) {
	now := time.Now()
	calls := 0
	a := &githubApp{client: &http.Client{Transport: githubTestTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		page := r.URL.Query().Get("page")
		var rows []string
		if page == "1" {
			for i := 1; i <= 30; i++ {
				rows = append(rows, fmt.Sprintf(`{"id":%d}`, i))
			}
		} else if page == "2" {
			rows = append(rows, `{"id":31}`)
		} else {
			t.Fatal("unexpected page")
		}
		body := `{"total_count":31,"workflows":[` + strings.Join(rows, ",") + `]}`
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}}
	items, err := githubActionsPages(context.Background(), a, githubToken{"synthetic", now.Add(time.Hour)}, "/repos/owner/repo/actions/workflows", nil, "workflows", now, func(w githubWorkflow) int64 { return w.ID })
	if err != nil || len(items) != 31 || calls != 2 {
		t.Fatal("pagination failed", err)
	}
}
