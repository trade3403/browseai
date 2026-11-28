package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGitHubSandboxExecutor(t *testing.T) {
	sha := strings.Repeat("a", 40)
	digest := strings.Repeat("b", 64)
	req := githubIsolatedRequest{
		statement: githubTaskExecutionStatement{
			Domain: githubTaskExecutionDomain,
			Execution: githubExecutionStatement{
				ExecutedSHA: sha, Run: 81, Attempt: 2, Jobs: []int64{91}, Required: []string{"gate"},
			},
		},
		harnessDigest: strings.Repeat("c", 64), imageDigest: digest,
	}
	plan := githubSandboxPlan{req, "sha256:" + digest, []githubIsolatedJob{{91, "gate", true}}}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	policy := githubSandboxBuildPolicy{
		builderID: "00000000-0000-4000-8000-000000000001", builderKey: public,
		baseImageDigest: strings.Repeat("d", 64), dependencyDigest: strings.Repeat("e", 64),
		sourceTreeDigest: strings.Repeat("f", 64),
	}
	request, err := githubSandboxRequestBytes(req)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(request)
	receipt := githubSandboxBuildReceipt{
		Domain: githubSandboxBuildDomain, Builder: policy.builderID, RequestDigest: hex.EncodeToString(sum[:]),
		CheckoutSHA: sha, SourceTreeDigest: policy.sourceTreeDigest, HarnessDigest: req.harnessDigest,
		BaseImageDigest: policy.baseImageDigest, DependencyDigest: policy.dependencyDigest, ImageDigest: digest,
		Jobs: []githubRemoteJob{{91, "gate", true}}, BuiltUnix: time.Now().Unix(),
	}
	sign := func(r githubSandboxBuildReceipt) githubSandboxBuildProof {
		t.Helper()
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		return githubSandboxBuildProof{r, ed25519.Sign(private, raw)}
	}
	proof := sign(receipt)

	calls := 0
	fail := false
	runner := func(ctx context.Context, name string, args []string) error {
		calls++
		if !strings.HasPrefix(name, "browseai-check-") || len(name) != len("browseai-check-")+32 {
			t.Fatal("unsafe container name")
		}
		expected := []string{
			"run", "--rm", "--name", name, "--pull=never", "--network=none",
			"--read-only", "--user=65532:65532", "--cap-drop=ALL",
			"--security-opt=no-new-privileges", "--pids-limit=128",
			"--memory=1g", "--memory-swap=1g", "--cpus=2",
			"--ipc=none", "--log-driver=none", "--no-healthcheck",
			"--tmpfs=/tmp:rw,noexec,nosuid,nodev,size=128m",
			"--entrypoint=/opt/browseai/trusted-test", plan.image,
		}
		if !reflect.DeepEqual(args, expected) {
			t.Fatal("unexpected sandbox flags", args)
		}
		if fail {
			return errors.New("failed test")
		}
		return nil
	}
	s, err := newGitHubSandboxExecutor(plan, policy, proof, runner)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	report, err := s.execute(ctx, req)
	if err != nil || !githubSupervisorReportValid(req, report) {
		t.Fatal("successful execution rejected", err)
	}
	bad := req
	bad.statement.Execution.Attempt++
	if _, err := s.execute(ctx, bad); err == nil || calls != 1 {
		t.Fatal("changed attempt executed")
	}
	bad = req
	bad.harnessDigest = digest
	if _, err := s.execute(ctx, bad); err == nil || calls != 1 {
		t.Fatal("changed harness executed")
	}
	fail = true
	if _, err := s.execute(ctx, req); err == nil || calls != 2 {
		t.Fatal("failed test accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.execute(cancelled, req); err == nil || calls != 2 {
		t.Fatal("cancelled test executed")
	}
	for _, image := range []string{"latest", "repo:latest", "sha256:" + strings.Repeat("d", 64), "--privileged"} {
		invalid := plan
		invalid.image = image
		if _, err := newGitHubSandboxExecutor(invalid, policy, proof, runner); err == nil {
			t.Fatal("unapproved image accepted", image)
		}
	}
	for _, mutate := range []func(*githubSandboxBuildReceipt){
		func(r *githubSandboxBuildReceipt) { r.Domain = "other" },
		func(r *githubSandboxBuildReceipt) { r.Builder = "00000000-0000-4000-8000-000000000002" },
		func(r *githubSandboxBuildReceipt) { r.RequestDigest = digest },
		func(r *githubSandboxBuildReceipt) { r.CheckoutSHA = strings.Repeat("d", 40) },
		func(r *githubSandboxBuildReceipt) { r.SourceTreeDigest = digest },
		func(r *githubSandboxBuildReceipt) { r.HarnessDigest = digest },
		func(r *githubSandboxBuildReceipt) { r.BaseImageDigest = digest },
		func(r *githubSandboxBuildReceipt) { r.DependencyDigest = digest },
		func(r *githubSandboxBuildReceipt) { r.ImageDigest = strings.Repeat("c", 64) },
		func(r *githubSandboxBuildReceipt) { r.Jobs = []githubRemoteJob{{91, "other", true}} },
		func(r *githubSandboxBuildReceipt) { r.BuiltUnix -= 86400 },
		func(r *githubSandboxBuildReceipt) { r.BuiltUnix += 3600 },
	} {
		bad := receipt
		mutate(&bad)
		if _, err := newGitHubSandboxExecutor(plan, policy, sign(bad), runner); err == nil {
			t.Fatal("invalid build provenance accepted")
		}
	}
	invalidProof := proof
	invalidProof.signature = make([]byte, ed25519.SignatureSize)
	if _, err := newGitHubSandboxExecutor(plan, policy, invalidProof, runner); err == nil {
		t.Fatal("unsigned build accepted")
	}
	otherPolicy := policy
	otherPolicy.builderKey = make([]byte, ed25519.PublicKeySize)
	if _, err := newGitHubSandboxExecutor(plan, otherPolicy, proof, runner); err == nil {
		t.Fatal("invalid builder key accepted")
	}
	// Expiry is checked again at execution, not only at provisioning.
	s.proof = sign(receipt)
	s.proof.receipt.BuiltUnix -= 86400
	if _, err := s.execute(ctx, req); err == nil || calls != 2 {
		t.Fatal("expired provenance reached runtime")
	}
	if _, err := githubDockerSandboxRunner("docker", "/run/docker.sock"); err == nil {
		t.Fatal("relative runtime accepted")
	}
}
