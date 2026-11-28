package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// A dedicated offline image is assembled by trusted infrastructure, not by
// the tested repository. Its digest pins the harness, dependencies and source.
// The operator must verify that relationship before provisioning this plan.
type githubSandboxPlan struct {
	// Each name and ID is independently approved and mapped to a fixed
	// image entrypoint invocation. Never derive commands from request text.
	request githubIsolatedRequest
	image   string
	jobs    []githubIsolatedJob
}

// This adapter is deliberately not wired into a production command. The
// trusted runtime, image provenance and host isolation need separate approval.
type githubSandboxExecutor struct {
	plan   githubSandboxPlan
	policy githubSandboxBuildPolicy
	proof  githubSandboxBuildProof
	run    func(context.Context, string, []string) error
}

func newGitHubSandboxExecutor(plan githubSandboxPlan, policy githubSandboxBuildPolicy, proof githubSandboxBuildProof, run func(context.Context, string, []string) error) (*githubSandboxExecutor, error) {
	if run == nil || !githubWorkflowDigestValid(plan.request.imageDigest) ||
		!githubWorkflowDigestValid(plan.request.harnessDigest) ||
		!taskSHA.MatchString(plan.request.statement.Execution.ExecutedSHA) ||
		!githubSandboxImageValid(plan.image, plan.request.imageDigest) {
		return nil, errGitHubAuthorization
	}
	report := githubIsolatedReport{
		checkoutSHA:   plan.request.statement.Execution.ExecutedSHA,
		harnessDigest: plan.request.harnessDigest, imageDigest: plan.request.imageDigest,
		jobs: plan.jobs,
	}
	if !githubSupervisorReportValid(plan.request, report) {
		return nil, errGitHubAuthorization
	}
	plan.jobs = append([]githubIsolatedJob(nil), plan.jobs...)
	plan.request.statement.Execution.Jobs = append([]int64(nil), plan.request.statement.Execution.Jobs...)
	plan.request.statement.Execution.Required = append([]string(nil), plan.request.statement.Execution.Required...)
	if err := verifyGitHubSandboxBuild(plan, policy, proof, time.Now()); err != nil {
		return nil, err
	}
	policy.builderKey = append([]byte(nil), policy.builderKey...)
	proof.signature = append([]byte(nil), proof.signature...)
	proof.receipt.Jobs = append([]githubRemoteJob(nil), proof.receipt.Jobs...)
	return &githubSandboxExecutor{plan, policy, proof, run}, nil
}

func githubSandboxImageValid(image, digest string) bool {
	// Only a local, content-addressed image ID is accepted. No tags, URLs or
	// remote pulls. This is the Docker image config digest (not a tag).
	return githubWorkflowDigestValid(digest) && image == "sha256:"+digest
}

func (s *githubSandboxExecutor) execute(ctx context.Context, req githubIsolatedRequest) (githubIsolatedReport, error) {
	fail := func() (githubIsolatedReport, error) { return githubIsolatedReport{}, errGitHubAuthorization }
	if s == nil || s.run == nil || ctx.Err() != nil {
		return fail()
	}
	// Compare the exact authorized request, including run/attempt and all jobs.
	expected, err := githubSandboxRequestBytes(s.plan.request)
	if err != nil {
		return fail()
	}
	actual, err := githubSandboxRequestBytes(req)
	if err != nil || !bytes.Equal(expected, actual) || verifyGitHubSandboxBuild(s.plan, s.policy, s.proof, time.Now()) != nil {
		return fail()
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(ctx, githubSupervisorTimeout)
	defer cancel()
	// A fixed trusted entrypoint runs the entire approved suite. It must
	// independently verify the baked checkout and harness, execute each
	// approved test, and exit nonzero on any failure. No stdout is parsed.
	name := "browseai-check-" + hex.EncodeToString(nonce[:])
	args := githubSandboxArgs(s.plan.image, name)
	if err := s.run(ctx, name, args); err != nil || ctx.Err() != nil {
		return fail()
	}
	return githubIsolatedReport{
		checkoutSHA:   req.statement.Execution.ExecutedSHA,
		harnessDigest: req.harnessDigest, imageDigest: req.imageDigest,
		jobs: append([]githubIsolatedJob(nil), s.plan.jobs...),
	}, nil
}

func githubSandboxRequestBytes(req githubIsolatedRequest) ([]byte, error) {
	return json.Marshal(githubRemoteExecutionRequest{githubRemoteExecutorDomain, "", req.statement, req.harnessDigest, req.imageDigest})
}

func githubSandboxArgs(image, name string) []string {
	return []string{
		"run", "--rm", "--name", name, "--pull=never", "--network=none",
		"--read-only", "--user=65532:65532", "--cap-drop=ALL",
		"--security-opt=no-new-privileges", "--pids-limit=128",
		"--memory=1g", "--memory-swap=1g", "--cpus=2",
		"--ipc=none", "--log-driver=none", "--no-healthcheck",
		"--tmpfs=/tmp:rw,noexec,nosuid,nodev,size=128m",
		"--entrypoint=/opt/browseai/trusted-test", image,
	}
}

// The daemon socket, executable and its parent paths MUST be under exclusive
// operator control. Use a dedicated, unprivileged rootless daemon in an
// isolated Linux VM; Docker flags alone are not a VM-grade security boundary.
func githubDockerSandboxRunner(binary, socket string) (func(context.Context, string, []string) error, error) {
	if runtime.GOOS != "linux" || !filepath.IsAbs(binary) || !filepath.IsAbs(socket) ||
		filepath.Clean(binary) != binary || filepath.Clean(socket) != socket ||
		strings.ContainsAny(socket, "\r\n") {
		return nil, errGitHubAuthorization
	}
	info, err := os.Stat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Mode().Perm()&0111 == 0 {
		return nil, errGitHubAuthorization
	}
	info, err = os.Stat(socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return nil, errGitHubAuthorization
	}
	command := func(ctx context.Context, stdout io.Writer, args ...string) error {
		cmd := exec.CommandContext(ctx, binary, append([]string{"--host=unix://" + socket}, args...)...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "DOCKER_CONFIG=/nonexistent"}
		cmd.Dir = "/"
		cmd.Stdout, cmd.Stderr = stdout, io.Discard
		cmd.WaitDelay = 5 * time.Second
		return cmd.Run()
	}
	return func(ctx context.Context, name string, args []string) error {
		// Do not run arbitrary argument lists, even from an internal caller.
		if len(args) == 0 || !strings.HasPrefix(name, "browseai-check-") {
			return errGitHubAuthorization
		}
		image := args[len(args)-1]
		if !githubSandboxImageValid(image, strings.TrimPrefix(image, "sha256:")) ||
			!slices.Equal(args, githubSandboxArgs(image, name)) {
			return errGitHubAuthorization
		}
		probe, stop := context.WithTimeout(ctx, 10*time.Second)
		defer stop()
		var info, inspect githubSandboxInspectBuffer
		if command(probe, &info, "info", "--format", "{{json .}}") != nil || !githubSandboxRuntimeValid(info.Bytes()) ||
			command(probe, &inspect, "image", "inspect", image) != nil || !githubSandboxImageConfigValid(inspect.Bytes(), image) {
			return errGitHubAuthorization
		}
		err := command(ctx, io.Discard, args...)
		if err == nil && ctx.Err() == nil {
			return nil
		}
		// Killing the client need not kill the daemon's container. Attempt
		// bounded cleanup independently; never treat an uncertain result as pass.
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cleanupErr := command(cleanup, io.Discard, "rm", "-f", name)
		return errors.Join(errGitHubAuthorization, err, ctx.Err(), cleanupErr)
	}, nil
}
