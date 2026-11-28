package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestGitHubExecutionProof(t *testing.T) {
	key, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p := githubEvidencePolicy{
		binding: githubBinding{"00000000-0000-4000-8000-000000000001", 12, 34, "owner/repo"},
		branch:  "feat/demo", sha: strings.Repeat("a", 40), workflowID: 56,
		name: "CI", path: ".github/workflows/ci.yml", required: []string{"gate"},
	}
	now := time.Unix(1800000000, 0)
	e := githubCIEvidence{p, 81, 2, []int64{91, 92}, now}
	pr := githubPRPolicy{push: p, number: 3, baseBranch: "main", baseSHA: strings.Repeat("b", 40)}
	rel := githubPRRelationship{3, p.sha, pr.baseSHA, strings.Repeat("c", 40)}
	digest := strings.Repeat("d", 64)
	sign := func(statement githubExecutionStatement) githubExecutionProof {
		t.Helper()
		raw, err := json.Marshal(statement)
		if err != nil {
			t.Fatal(err)
		}
		return githubExecutionProof{statement.IssuedUnix, ed25519.Sign(private, raw)}
	}
	statement := githubExecutionMessage(p, e, digest, &pr, rel, now.Unix())
	proof := sign(statement)
	if err := verifyGitHubExecution(p, e, digest, &pr, rel, proof, key, now); err != nil {
		t.Fatal("valid proof rejected", err)
	}
	for _, change := range []func(*githubExecutionStatement){
		func(s *githubExecutionStatement) { s.Domain = "other" },
		func(s *githubExecutionStatement) { s.ExecutedSHA = p.sha },
		func(s *githubExecutionStatement) { s.BaseSHA = p.sha },
		func(s *githubExecutionStatement) { s.PR++ },
		func(s *githubExecutionStatement) { s.Attempt++ },
		func(s *githubExecutionStatement) { s.Run++ },
		func(s *githubExecutionStatement) { s.Repository++ },
		func(s *githubExecutionStatement) { s.SourceDigest = strings.Repeat("e", 64) },
		func(s *githubExecutionStatement) { s.Jobs = []int64{91} },
		func(s *githubExecutionStatement) { s.Required = []string{"other"} },
		func(s *githubExecutionStatement) { s.IssuedUnix += 60 },
		func(s *githubExecutionStatement) { s.IssuedUnix -= 86400 },
	} {
		bad := statement
		change(&bad)
		if err := verifyGitHubExecution(p, e, digest, &pr, rel, sign(bad), key, now); err == nil {
			t.Fatal("mismatched proof accepted")
		}
	}
	if err := verifyGitHubExecution(p, e, digest, nil, githubPRRelationship{}, proof, key, now); err == nil {
		t.Fatal("PR proof accepted as push")
	}
	push := sign(githubExecutionMessage(p, e, digest, nil, githubPRRelationship{}, now.Unix()))
	if err := verifyGitHubExecution(p, e, digest, nil, githubPRRelationship{}, push, key, now); err != nil {
		t.Fatal("push proof rejected", err)
	}
	if err := verifyGitHubExecution(p, e, digest, &pr, rel, githubExecutionProof{}, key, now); err == nil {
		t.Fatal("missing proof accepted")
	}
	if err := verifyGitHubExecution(p, e, digest, &pr, rel, proof, key, now.Add(githubEvidenceLifetime)); err == nil {
		t.Fatal("stale remote observation accepted")
	}
	other, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyGitHubExecution(p, e, digest, &pr, rel, proof, other, now); err == nil {
		t.Fatal("untrusted signer accepted")
	}
}
