package main

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
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
			"--ipc=none", "--log-driver=none",
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
	s, err := newGitHubSandboxExecutor(plan, runner)
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
		if _, err := newGitHubSandboxExecutor(invalid, runner); err == nil {
			t.Fatal("unapproved image accepted", image)
		}
	}
	if _, err := githubDockerSandboxRunner("docker", "/run/docker.sock"); err == nil {
		t.Fatal("relative runtime accepted")
	}
}
