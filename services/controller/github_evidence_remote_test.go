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

func TestGitHubEvidenceRemoteSnapshots(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	head, base, merge := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	p := githubPRPolicy{
		push: githubEvidencePolicy{
			binding: githubBinding{"00000000-0000-4000-8000-000000000001", 123, 456, "owner/repo"},
			branch:  "feat/demo", sha: head, workflowID: 2,
			name: "CI", path: ".github/workflows/ci.yml", required: []string{"gate"},
		},
		number: 3, baseBranch: "main", baseSHA: base,
	}
	headRef := `{"ref":"feat/demo","sha":"` + head + `","repo":{"id":456}}`
	baseRef := `{"ref":"main","sha":"` + base + `","repo":{"id":456}}`
	pr := `{"number":3,"state":"open","merged":false,"mergeable":true,"merge_commit_sha":"` + merge + `","head":` + headRef + `,"base":` + baseRef + `}`
	link := `{"number":3,"head":` + headRef + `,"base":` + baseRef + `}`
	run := `{"id":81,"workflow_id":2,"run_attempt":2,"head_sha":"` + head + `","head_branch":"feat/demo","event":"pull_request","status":"completed","conclusion":"success","repository":{"id":456},"head_repository":{"id":456},"path":".github/workflows/ci.yml","pull_requests":[` + link + `]}`
	workflow := `{"total_count":1,"workflows":[{"id":2,"name":"CI","path":".github/workflows/ci.yml","state":"active"}]}`
	job := `{"total_count":1,"jobs":[{"id":91,"run_id":81,"run_attempt":2,"head_sha":"` + head + `","name":"gate","status":"completed","conclusion":"success"}]}`
	mode := ""
	prReads, listReads, detailReads := 0, 0, 0
	a, err := newGitHubApp("mock-app", key, githubTestTransport(func(r *http.Request) (*http.Response, error) {
		var body string
		status := 200
		switch r.URL.Path {
		case "/app/installations/123/access_tokens":
			status = 201
			body = `{"token":"synthetic","expires_at":"2026-11-27T01:00:00Z","repositories":[{"id":456}],"permissions":{"actions":"read","checks":"read","contents":"read","pull_requests":"read","metadata":"read"}}`
		case "/repos/owner/repo":
			body = `{"id":456,"full_name":"owner/repo"}`
		case "/repos/owner/repo/pulls/3":
			prReads++
			body = pr
			if mode == "pr-changed" && prReads == 2 {
				body = strings.Replace(body, `"state":"open"`, `"state":"closed"`, 1)
			}
			if mode == "pr-missing" {
				status = 404
			}
		case "/repos/owner/repo/git/commits/" + merge:
			body = `{"sha":"` + merge + `","parents":[{"sha":"` + base + `"},{"sha":"` + head + `"}]}`
			if mode == "merge-error" {
				status = 503
			}
		case "/repos/owner/repo/actions/workflows":
			body = workflow
		case "/repos/owner/repo/actions/runs":
			listReads++
			if r.URL.Query().Get("head_sha") != head || r.URL.Query().Get("branch") != "feat/demo" {
				t.Fatal("wrong run query")
			}
			item := run
			if mode == "push" {
				item = strings.Replace(item, `"pull_request"`, `"push"`, 1)
			}
			body = `{"total_count":1,"workflow_runs":[` + item + `]}`
			if mode == "ambiguous" || (mode == "new-run" && listReads == 2) {
				body = `{"total_count":2,"workflow_runs":[` + item + "," + strings.Replace(item, `"id":81`, `"id":82`, 1) + `]}`
			}
		case "/repos/owner/repo/actions/runs/81":
			detailReads++
			body = run
			if mode == "push" {
				body = strings.Replace(body, `"pull_request"`, `"push"`, 1)
			}
			if mode == "foreign-link" {
				body = strings.Replace(body, `"number":3`, `"number":4`, 1)
			}
			if mode == "late-link" && detailReads == 4 {
				body = strings.Replace(body, `"number":3`, `"number":4`, 1)
			}
			if mode == "wrong-id" {
				body = strings.Replace(body, `"id":81`, `"id":82`, 1)
			}
		case "/repos/owner/repo/actions/runs/81/attempts/2/jobs":
			body = job
			if mode == "jobs-error" {
				status = 503
			}
		default:
			t.Fatal("unexpected endpoint", r.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	clock := func() time.Time { return now }
	candidate, err := a.observePRCandidate(ctx, p, clock)
	if err != nil || candidate.relationship.mergeSHA != merge || candidate.runID != 81 || len(candidate.jobIDs) != 1 || prReads != 4 || listReads != 2 || detailReads != 4 {
		t.Fatal("valid PR snapshot rejected", err, prReads, listReads, detailReads)
	}
	for _, bad := range []string{"pr-changed", "pr-missing", "merge-error", "ambiguous", "new-run", "foreign-link", "late-link", "wrong-id", "jobs-error"} {
		t.Run(bad, func(t *testing.T) {
			mode = bad
			prReads, listReads, detailReads = 0, 0, 0
			if _, err := a.observePRCandidate(ctx, p, clock); err == nil {
				t.Fatal("invalid remote snapshot accepted")
			}
		})
	}
	mode = "push"
	e, err := a.observePushEvidence(ctx, p.push, clock)
	if err != nil || !e.freshFor(p.push, now) {
		t.Fatal("valid push snapshot rejected", err)
	}
	if _, err := a.observePRCandidate(ctx, p, clock); err == nil {
		t.Fatal("push accepted as PR")
	}
	mode = ""
	if _, err := a.observePushEvidence(ctx, p.push, clock); err == nil {
		t.Fatal("PR accepted as push")
	}
	if _, err := a.observePRCandidate(ctx, p, nil); err == nil {
		t.Fatal("nil clock accepted")
	}
	if _, err := a.observePRRelationship(ctx, p, nil); err == nil {
		t.Fatal("nil relationship clock accepted")
	}
	if _, err := a.observePushEvidence(ctx, p.push, nil); err == nil {
		t.Fatal("nil push clock accepted")
	}
}
