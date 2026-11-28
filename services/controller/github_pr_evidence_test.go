package main

import (
	"strings"
	"testing"
)

func TestGitHubPRRelationship(t *testing.T) {
	p := githubPRPolicy{
		push: githubEvidencePolicy{
			binding: githubBinding{"00000000-0000-4000-8000-000000000001", 12, 34, "owner/repo"},
			branch:  "feat/demo", sha: strings.Repeat("a", 40), workflowID: 56,
			name: "CI", path: ".github/workflows/ci.yml", required: []string{"gate"},
		},
		number: 3, baseBranch: "main", baseSHA: strings.Repeat("b", 40),
	}
	yes := true
	pr := githubPullSnapshot{Number: 3, State: "open", Mergeable: &yes, MergeCommitSHA: strings.Repeat("c", 40)}
	pr.Head.Ref, pr.Head.SHA, pr.Head.Repo.ID = p.push.branch, p.push.sha, 34
	pr.Base.Ref, pr.Base.SHA, pr.Base.Repo.ID = p.baseBranch, p.baseSHA, 34
	var merge githubMergeCommit
	merge.SHA = pr.MergeCommitSHA
	merge.Parents = append(merge.Parents, struct {
		SHA string `json:"sha"`
	}{p.baseSHA}, struct {
		SHA string `json:"sha"`
	}{p.push.sha})
	got, err := validateGitHubPRRelationship(p, pr, merge)
	if err != nil || got.mergeSHA != merge.SHA {
		t.Fatal("valid PR relationship rejected", err)
	}
	for _, change := range []func(*githubPullSnapshot){
		func(v *githubPullSnapshot) { v.Number++ },
		func(v *githubPullSnapshot) { v.State = "closed" },
		func(v *githubPullSnapshot) { v.Merged = true },
		func(v *githubPullSnapshot) { v.Mergeable = nil },
		func(v *githubPullSnapshot) { no := false; v.Mergeable = &no },
		func(v *githubPullSnapshot) { v.Head.Repo.ID++ },
		func(v *githubPullSnapshot) { v.Base.Repo.ID++ },
		func(v *githubPullSnapshot) { v.Head.Ref = "feat/other" },
		func(v *githubPullSnapshot) { v.Base.Ref = "other" },
		func(v *githubPullSnapshot) { v.Head.SHA = strings.Repeat("d", 40) },
		func(v *githubPullSnapshot) { v.Base.SHA = strings.Repeat("d", 40) },
		func(v *githubPullSnapshot) { v.MergeCommitSHA = "" },
	} {
		bad := pr
		change(&bad)
		if _, err := validateGitHubPRRelationship(p, bad, merge); err == nil {
			t.Fatal("foreign PR accepted")
		}
	}
	bad := merge
	bad.Parents = bad.Parents[:1]
	if _, err := validateGitHubPRRelationship(p, pr, bad); err == nil {
		t.Fatal("single parent accepted")
	}
	merge.Parents[0], merge.Parents[1] = merge.Parents[1], merge.Parents[0]
	if _, err := validateGitHubPRRelationship(p, pr, merge); err == nil {
		t.Fatal("reversed parents accepted")
	}
}
