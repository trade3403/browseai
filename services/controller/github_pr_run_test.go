package main

import (
	"strings"
	"testing"
	"time"
)

func TestGitHubPRRunCandidate(t *testing.T) {
	p := githubPRPolicy{
		push: githubEvidencePolicy{
			binding: githubBinding{"00000000-0000-4000-8000-000000000001", 12, 34, "owner/repo"},
			branch:  "feat/demo", sha: strings.Repeat("a", 40), workflowID: 56,
			name: "CI", path: ".github/workflows/ci.yml", required: []string{"gate"},
		},
		number: 3, baseBranch: "main", baseSHA: strings.Repeat("b", 40),
	}
	rel := githubPRRelationship{3, p.push.sha, p.baseSHA, strings.Repeat("c", 40)}
	w := githubWorkflow{56, "CI", p.push.path, "active"}
	r := githubRun{ID: 78, WorkflowID: 56, Attempt: 2, HeadSHA: p.push.sha, HeadBranch: p.push.branch, Event: "pull_request", Status: "completed", Conclusion: "success"}
	r.Repository.ID, r.HeadRepository.ID = 34, 34
	link := githubRunPRLink{Number: 3}
	link.Head.Ref, link.Head.SHA, link.Head.Repo.ID = p.push.branch, p.push.sha, 34
	link.Base.Ref, link.Base.SHA, link.Base.Repo.ID = p.baseBranch, p.baseSHA, 34
	snapshot := githubPRRunSnapshot{r, p.push.path, []githubRunPRLink{link}}
	jobs := []githubJob{{91, 78, 2, p.push.sha, "gate", "completed", "success"}}
	now := time.Now()
	got, err := makeGitHubPRCandidate(p, rel, w, snapshot, jobs, now)
	if err != nil || got.relationship != rel || got.runID != 78 || got.attempt != 2 || len(got.jobIDs) != 1 {
		t.Fatal("valid association rejected", err)
	}
	for _, change := range []func(*githubPRRunSnapshot){
		func(s *githubPRRunSnapshot) { s.Event = "push" },
		func(s *githubPRRunSnapshot) { s.HeadSHA = rel.mergeSHA },
		func(s *githubPRRunSnapshot) { s.HeadRepository.ID++ },
		func(s *githubPRRunSnapshot) { s.Path = ".github/workflows/other.yml" },
		func(s *githubPRRunSnapshot) { s.Attempt++ },
		func(s *githubPRRunSnapshot) { s.PullRequests = nil },
		func(s *githubPRRunSnapshot) { s.PullRequests = append(s.PullRequests, link) },
		func(s *githubPRRunSnapshot) { s.PullRequests[0].Number++ },
		func(s *githubPRRunSnapshot) { s.PullRequests[0].Base.SHA = strings.Repeat("d", 40) },
		func(s *githubPRRunSnapshot) { s.PullRequests[0].Head.Repo.ID++ },
		func(s *githubPRRunSnapshot) { s.PullRequests[0].Base.Ref = "other" },
	} {
		bad := snapshot
		bad.PullRequests = append([]githubRunPRLink(nil), snapshot.PullRequests...)
		change(&bad)
		if _, err := makeGitHubPRCandidate(p, rel, w, bad, jobs, now); err == nil {
			t.Fatal("foreign run accepted")
		}
	}
	if _, err := makeGitHubPRCandidate(p, rel, w, snapshot, nil, now); err == nil {
		t.Fatal("missing jobs accepted")
	}
	badRel := rel
	badRel.baseSHA = strings.Repeat("d", 40)
	if _, err := makeGitHubPRCandidate(p, badRel, w, snapshot, jobs, now); err == nil {
		t.Fatal("stale relationship accepted")
	}
}
