package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGitHubAttemptJobs(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	binding := githubBinding{"00000000-0000-4000-8000-000000000001", 123, 456, "owner/repo"}
	const sha = "0123456789abcdef0123456789abcdef01234567"
	const runBody = `{"id":81,"workflow_id":2,"run_attempt":2,"head_sha":"` + sha + `","head_branch":"feat/demo","event":"push","status":"completed","conclusion":"success","repository":{"id":456},"head_repository":{"id":456}}`
	const job = `{"id":91,"run_id":81,"run_attempt":2,"head_sha":"` + sha + `","name":"gate","status":"completed","conclusion":"success"}`
	var run githubRun
	if err := json.Unmarshal([]byte(runBody), &run); err != nil {
		t.Fatal(err)
	}
	jobBody := `{"total_count":1,"jobs":[` + job + `]}`
	stale := false
	reads := 0
	a, err := newGitHubApp("mock-app", key, githubTestTransport(func(r *http.Request) (*http.Response, error) {
		var body string
		status := 200
		switch r.URL.Path {
		case "/app/installations/123/access_tokens":
			status = 201
			body = `{"token":"synthetic","expires_at":"2026-11-27T01:00:00Z","repositories":[{"id":456}],"permissions":{"actions":"read","checks":"read","contents":"read","pull_requests":"read","metadata":"read"}}`
		case "/repos/owner/repo/actions/runs/81":
			reads++
			body = runBody
			if stale && reads%2 == 0 {
				body = strings.Replace(body, `"run_attempt":2`, `"run_attempt":3`, 1)
			}
		case "/repos/owner/repo/actions/runs/81/attempts/2/jobs":
			body = jobBody
		default:
			t.Fatal("unexpected endpoint", r.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	jobs, err := a.listAttemptJobs(ctx, binding, run, now)
	if err != nil || len(jobs) != 1 || classifyGitHubCI(run, jobs, []string{"gate"}) != githubCIPassed {
		t.Fatal("valid attempt rejected", err)
	}
	for _, bad := range []string{
		strings.Replace(job, `"run_attempt":2`, `"run_attempt":1`, 1),
		strings.Replace(job, `"run_id":81`, `"run_id":82`, 1),
		strings.Replace(job, sha, strings.Repeat("a", 40), 1),
	} {
		jobBody = `{"total_count":1,"jobs":[` + bad + `]}`
		if _, err := a.listAttemptJobs(ctx, binding, run, now); err == nil {
			t.Fatal("foreign attempt accepted")
		}
	}
	jobBody = `{"total_count":1,"jobs":[` + job + `]}`
	reads = 0
	stale = true
	if _, err := a.listAttemptJobs(ctx, binding, run, now); err == nil {
		t.Fatal("superseded attempt accepted")
	}
}

func TestGitHubCIClassification(t *testing.T) {
	run := githubRun{ID: 1, Attempt: 2, HeadSHA: strings.Repeat("a", 40), Status: "completed", Conclusion: "success"}
	job := githubJob{1, 1, 2, run.HeadSHA, "gate", "completed", "success"}
	for _, required := range [][]string{nil, {"missing"}, {"gate", "gate"}} {
		if classifyGitHubCI(run, []githubJob{job}, required) != githubCIUnknown {
			t.Fatal("missing or invalid required jobs accepted")
		}
	}
	if classifyGitHubCI(run, []githubJob{job, job}, []string{"gate"}) != githubCIUnknown {
		t.Fatal("duplicate job accepted")
	}
	for _, conclusion := range []string{"skipped", "neutral", "cancelled", "failure", ""} {
		bad := job
		bad.Conclusion = conclusion
		if classifyGitHubCI(run, []githubJob{bad}, []string{"gate"}) != githubCIUnknown {
			t.Fatal("non-success job accepted")
		}
	}
	run.Conclusion = "failure"
	if classifyGitHubCI(run, nil, nil) != githubCIFailed {
		t.Fatal("failure lost")
	}
	run.Conclusion = "cancelled"
	if classifyGitHubCI(run, nil, nil) != githubCICancel {
		t.Fatal("cancellation lost")
	}
	run.Status, run.Conclusion = "in_progress", ""
	if classifyGitHubCI(run, nil, nil) != githubCIPending {
		t.Fatal("pending lost")
	}
}
