package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"time"
)

type githubMockIsolatedExecutor func(context.Context, githubIsolatedRequest) (githubIsolatedReport, error)

func (f githubMockIsolatedExecutor) execute(ctx context.Context, r githubIsolatedRequest) (githubIsolatedReport, error) {
	return f(ctx, r)
}

func TestGitHubSupervisor(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	supervisorPublic, supervisorPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1800000000, 0)
	clock := func() time.Time { return now }
	p := githubEvidencePolicy{
		binding: githubBinding{"00000000-0000-4000-8000-000000000001", 12, 34, "owner/repo"},
		branch:  "feat/demo", sha: strings.Repeat("a", 40), workflowID: 56,
		name: "CI", path: ".github/workflows/ci.yml", required: []string{"gate"},
	}
	policy := githubIndependentPolicy{
		task:      githubEvidenceTask{"00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000003", 4},
		execution: p, sourceDigest: strings.Repeat("d", 64),
		harnessDigest: strings.Repeat("e", 64), imageDigest: strings.Repeat("f", 64),
		verifierID: "00000000-0000-4000-8000-000000000004",
		keyID:      "00000000-0000-4000-8000-000000000005",
	}
	rel := githubPRRelationship{}
	mode := ""
	calls := 0
	observe := func(ctx context.Context) (githubCIEvidence, githubPRRelationship, error) {
		calls++
		e := githubCIEvidence{p, 81, 2, []int64{91, 92}, now}
		if calls == 2 && mode == "changed" {
			e.attempt++
		}
		if calls == 2 && mode == "unavailable" {
			return e, rel, errors.New("unavailable")
		}
		return e, rel, nil
	}
	executor := githubMockIsolatedExecutor(func(ctx context.Context, req githubIsolatedRequest) (githubIsolatedReport, error) {
		if req.statement.Task != policy.task.taskID || req.statement.Execution.IssuedUnix != 0 {
			t.Fatal("wrong request identity")
		}
		// The execution can outlive the observation freshness window.
		now = now.Add(time.Minute)
		report := githubIsolatedReport{
			checkoutSHA:   req.statement.Execution.ExecutedSHA,
			harnessDigest: req.harnessDigest, imageDigest: req.imageDigest,
			jobs: []githubIsolatedJob{{91, "backend", true}, {92, "gate", true}},
		}
		switch mode {
		case "failed":
			report.jobs[0].passed = false
		case "missing":
			report.jobs = report.jobs[:1]
		case "duplicate":
			report.jobs[1].id = 91
		case "name":
			report.jobs[1].name = "other"
		case "checkout":
			report.checkoutSHA = strings.Repeat("b", 40)
		case "harness":
			report.harnessDigest = strings.Repeat("a", 64)
		case "image":
			report.imageDigest = strings.Repeat("a", 64)
		case "timeout":
			now = now.Add(githubSupervisorTimeout)
		case "error":
			return report, errors.New("executor failure")
		case "mutate":
			req.statement.Execution.Jobs[0] = 999
			report.jobs[0].id = 999
		}
		return report, nil
	})
	s := githubSupervisor{supervisorPrivate, executor}
	ctx := context.Background()
	for _, bad := range []string{"failed", "missing", "duplicate", "name", "checkout", "harness", "image", "timeout", "error", "mutate", "changed", "unavailable"} {
		mode, calls = bad, 0
		if _, sig, err := s.run(ctx, policy, observe, clock); err == nil || sig != nil {
			t.Fatal("invalid execution signed", bad)
		}
	}
	mode, calls = "", 0
	receipt, sig, err := s.run(ctx, policy, observe, clock)
	if err != nil || calls != 2 {
		t.Fatal("successful execution rejected", err, calls)
	}
	e := githubCIEvidence{p, 81, 2, []int64{91, 92}, now}
	signer := githubIndependentSigner{private, supervisorPublic}
	proof, err := signer.sign(policy, e, rel, receipt, sig, now)
	if err != nil {
		t.Fatal("receipt rejected", err)
	}
	if _, err := verifyGitHubTaskExecution(policy.task, p, e, policy.sourceDigest, nil, rel, proof, public, now); err != nil {
		t.Fatal("controller rejected proof", err)
	}
	pr := githubPRPolicy{push: p, number: 3, baseBranch: "main", baseSHA: strings.Repeat("b", 40)}
	policy.pr = &pr
	rel = githubPRRelationship{3, p.sha, pr.baseSHA, strings.Repeat("c", 40)}
	calls = 0
	receipt, sig, err = s.run(ctx, policy, observe, clock)
	if err != nil || receipt.CheckoutSHA != rel.mergeSHA {
		t.Fatal("merge execution rejected", err)
	}
	if _, err := signer.sign(policy, githubCIEvidence{p, 81, 2, []int64{91, 92}, now}, rel, receipt, sig, now); err != nil {
		t.Fatal("PR receipt rejected", err)
	}
	if _, _, err := (githubSupervisor{}).run(ctx, policy, observe, clock); err == nil {
		t.Fatal("missing executor accepted")
	}
}
