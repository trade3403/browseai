package main

import (
	"strings"
	"testing"
	"time"
)

func TestGitHubPushEvidence(t *testing.T) {
	p := githubEvidencePolicy{
		binding: githubBinding{"00000000-0000-4000-8000-000000000001", 12, 34, "owner/repo"},
		branch:  "feat/demo", sha: strings.Repeat("a", 40), workflowID: 56,
		name: "CI", path: ".github/workflows/ci.yml", required: []string{"gate", "backend"},
	}
	w := githubWorkflow{56, "CI", ".github/workflows/ci.yml", "active"}
	r := githubRun{ID: 78, WorkflowID: 56, Attempt: 2, HeadSHA: p.sha, HeadBranch: p.branch, Event: "push", Status: "completed", Conclusion: "success"}
	r.Repository.ID, r.HeadRepository.ID = 34, 34
	jobs := []githubJob{{91, 78, 2, p.sha, "gate", "completed", "success"}, {90, 78, 2, p.sha, "backend", "completed", "success"}}
	now := time.Now()
	e, err := makeGitHubPushEvidence(p, w, r, jobs, now)
	if err != nil || !e.freshFor(p, now) || e.jobIDs[0] != 90 {
		t.Fatal("valid evidence rejected", err)
	}
	if e.freshFor(p, now.Add(githubEvidenceLifetime)) || e.freshFor(p, now.Add(-time.Second)) {
		t.Fatal("invalid observation time accepted")
	}
	for _, change := range []func(*githubEvidencePolicy){
		func(p *githubEvidencePolicy) { p.sha = strings.Repeat("b", 40) },
		func(p *githubEvidencePolicy) { p.branch = "feat/other" },
		func(p *githubEvidencePolicy) { p.binding.repositoryID++ },
		func(p *githubEvidencePolicy) { p.binding.installationID++ },
		func(p *githubEvidencePolicy) { p.workflowID++ },
		func(p *githubEvidencePolicy) { p.name = "Other" },
		func(p *githubEvidencePolicy) { p.path = ".github/workflows/other.yml" },
		func(p *githubEvidencePolicy) { p.required = []string{"gate"} },
	} {
		bad := p
		change(&bad)
		if e.freshFor(bad, now) {
			t.Fatal("foreign policy accepted")
		}
	}
	for _, change := range []func(*githubRun){
		func(r *githubRun) { r.Event = "pull_request" },
		func(r *githubRun) { r.HeadSHA = strings.Repeat("b", 40) },
		func(r *githubRun) { r.HeadBranch = "feat/other" },
		func(r *githubRun) { r.Attempt++ },
		func(r *githubRun) { r.Repository.ID++ },
		func(r *githubRun) { r.HeadRepository.ID++ },
		func(r *githubRun) { r.Conclusion = "failure" },
	} {
		bad := r
		change(&bad)
		if _, err := makeGitHubPushEvidence(p, w, bad, jobs, now); err == nil {
			t.Fatal("foreign run accepted")
		}
	}
	for _, change := range []func(*githubWorkflow){
		func(w *githubWorkflow) { w.ID++ },
		func(w *githubWorkflow) { w.Name = "Other" },
		func(w *githubWorkflow) { w.Path = ".github/workflows/other.yml" },
		func(w *githubWorkflow) { w.State = "disabled_manually" },
	} {
		bad := w
		change(&bad)
		if _, err := makeGitHubPushEvidence(p, bad, r, jobs, now); err == nil {
			t.Fatal("foreign workflow accepted")
		}
	}
	if _, err := makeGitHubPushEvidence(p, w, r, jobs[:1], now); err == nil {
		t.Fatal("incomplete jobs accepted")
	}
	p.required[0] = "changed"
	if e.freshFor(p, now) {
		t.Fatal("evidence policy mutated through caller")
	}
}
