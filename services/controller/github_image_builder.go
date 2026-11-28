package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// The builder runs separately from the node and repository workloads.
// Its daemon/VM must be offline even during base image resolution: Docker
// --network=none and --pull=false alone do not guarantee offline builds.
type githubImageBuildBackend interface {
	build(context.Context, string, io.Reader) (string, []byte, error)
}

func githubBuildSandboxImage(ctx context.Context, plan githubSandboxPlan, materials githubBuildMaterials, policy githubSandboxBuildPolicy, private ed25519.PrivateKey, staging string, backend githubImageBuildBackend) (githubSandboxPlan, githubSandboxBuildProof, error) {
	fail := func() (githubSandboxPlan, githubSandboxBuildProof, error) {
		return githubSandboxPlan{}, githubSandboxBuildProof{}, errGitHubAuthorization
	}
	if runtime.GOOS != "linux" || ctx.Err() != nil || backend == nil ||
		len(private) != ed25519.PrivateKeySize || !bytes.Equal(private, ed25519.NewKeyFromSeed(private.Seed())) ||
		!bytes.Equal(private.Public().(ed25519.PublicKey), policy.builderKey) ||
		!resourceUUID.MatchString(policy.builderID) ||
		!taskSHA.MatchString(plan.request.statement.Execution.ExecutedSHA) ||
		plan.request.statement.Domain != githubTaskExecutionDomain || plan.request.statement.Execution.IssuedUnix != 0 ||
		!githubWorkflowDigestValid(plan.request.harnessDigest) || plan.image != "" || plan.request.imageDigest != "" {
		return fail()
	}
	// Verify the job mapping before invoking any builder. These are approved
	// test identities, not claims that the tests have already run.
	if len(plan.jobs) == 0 || len(plan.jobs) > 1000 ||
		!githubSupervisorReportValid(plan.request, githubIsolatedReport{plan.request.statement.Execution.ExecutedSHA, plan.request.harnessDigest, "", plan.jobs}) {
		return fail()
	}
	plan.jobs = append([]githubIsolatedJob(nil), plan.jobs...)
	plan.request.statement.Execution.Jobs = append([]int64(nil), plan.request.statement.Execution.Jobs...)
	plan.request.statement.Execution.Required = append([]string(nil), plan.request.statement.Execution.Required...)
	if _, err := newGitHubExecutionGrantStore(staging); err != nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(ctx, githubSupervisorTimeout)
	defer cancel()
	file, err := os.CreateTemp(staging, "build-context-*.tar")
	if err != nil {
		return fail()
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err := githubWriteBuildContext(ctx, file, materials, policy, plan.request.harnessDigest); err != nil {
		return fail()
	}
	if err := file.Sync(); err != nil {
		return fail()
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fail()
	}
	base := materials.baseName + "@sha256:" + policy.baseImageDigest
	id, inspect, err := backend.build(ctx, base, file)
	if err != nil || ctx.Err() != nil || !githubSandboxImageValid(id, strings.TrimPrefix(id, "sha256:")) ||
		!githubSandboxImageConfigValid(inspect, id) {
		return fail()
	}
	plan.image = id
	plan.request.imageDigest = strings.TrimPrefix(id, "sha256:")
	raw, err := githubSandboxRequestBytes(plan.request)
	if err != nil {
		return fail()
	}
	sum := sha256.Sum256(raw)
	now := time.Now()
	receipt := githubSandboxBuildReceipt{
		Domain: githubSandboxBuildDomain, Builder: policy.builderID, RequestDigest: hex.EncodeToString(sum[:]),
		CheckoutSHA: plan.request.statement.Execution.ExecutedSHA, SourceTreeDigest: policy.sourceTreeDigest,
		HarnessDigest: plan.request.harnessDigest, BaseImageDigest: policy.baseImageDigest,
		DependencyDigest: policy.dependencyDigest, ImageDigest: plan.request.imageDigest, BuiltUnix: now.Unix(),
	}
	for _, j := range plan.jobs {
		receipt.Jobs = append(receipt.Jobs, githubRemoteJob{j.id, j.name, j.passed})
	}
	payload, err := json.Marshal(receipt)
	if err != nil || len(payload) > githubRemoteBodyLimit || ctx.Err() != nil {
		return fail()
	}
	proof := githubSandboxBuildProof{receipt, ed25519.Sign(private, payload)}
	if verifyGitHubSandboxBuild(plan, policy, proof, now) != nil || ctx.Err() != nil {
		return fail()
	}
	return plan, proof, nil
}

type githubDockerImageBuilder struct {
	binary  string
	socket  string
	staging string
}

func newGitHubDockerImageBuilder(binary, socket, staging string) (*githubDockerImageBuilder, error) {
	if _, err := githubDockerSandboxRunner(binary, socket); err != nil {
		return nil, err
	}
	if _, err := newGitHubExecutionGrantStore(staging); err != nil {
		return nil, err
	}
	return &githubDockerImageBuilder{binary, socket, staging}, nil
}

func githubBuildBaseValid(raw []byte, base string) bool {
	if len(raw) == 0 || len(raw) > githubSandboxInspectLimit {
		return false
	}
	var images []struct {
		OS          string
		RepoDigests []string
		Config      *struct {
			OnBuild []string
			Volumes map[string]json.RawMessage
		}
	}
	if json.Unmarshal(raw, &images) != nil || len(images) != 1 || images[0].OS != "linux" ||
		images[0].Config == nil || len(images[0].Config.OnBuild) != 0 || len(images[0].Config.Volumes) != 0 {
		return false
	}
	for _, digest := range images[0].RepoDigests {
		if digest == base {
			return true
		}
	}
	return false
}

func (b *githubDockerImageBuilder) build(ctx context.Context, base string, input io.Reader) (string, []byte, error) {
	fail := func() (string, []byte, error) { return "", nil, errGitHubAuthorization }
	if b == nil || input == nil || ctx.Err() != nil {
		return fail()
	}
	name, digest, ok := strings.Cut(base, "@sha256:")
	if !ok {
		return fail()
	}
	if _, err := githubBuildDockerfile(name, digest); err != nil {
		return fail()
	}
	if _, err := newGitHubDockerImageBuilder(b.binary, b.socket, b.staging); err != nil {
		return fail()
	}
	command := func(ctx context.Context, stdin io.Reader, stdout io.Writer, args ...string) error {
		cmd := exec.CommandContext(ctx, b.binary, append([]string{"--host=unix://" + b.socket}, args...)...)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "DOCKER_CONFIG=/nonexistent", "DOCKER_BUILDKIT=1"}
		cmd.Dir = "/"
		cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, io.Discard
		cmd.WaitDelay = 5 * time.Second
		return cmd.Run()
	}
	var info, baseInfo githubSandboxInspectBuffer
	probe, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	if command(probe, nil, &info, "info", "--format", "{{json .}}") != nil || !githubSandboxRuntimeValid(info.Bytes()) ||
		command(probe, nil, &baseInfo, "image", "inspect", base) != nil || !githubBuildBaseValid(baseInfo.Bytes(), base) {
		return fail()
	}
	// Only a private staging directory can hold the build result ID.
	dir, err := os.MkdirTemp(b.staging, "image-build-")
	if err != nil {
		return fail()
	}
	defer os.RemoveAll(dir)
	iid := filepath.Join(dir, "image-id")
	if command(ctx, input, io.Discard, "build", "--network=none", "--pull=false", "--no-cache", "--iidfile", iid, "-") != nil || ctx.Err() != nil {
		return fail()
	}
	file, err := os.Open(iid)
	if err != nil {
		return fail()
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 128))
	id := strings.TrimSpace(string(raw))
	if err != nil || !githubSandboxImageValid(id, strings.TrimPrefix(id, "sha256:")) {
		return fail()
	}
	var inspect githubSandboxInspectBuffer
	if command(ctx, nil, &inspect, "image", "inspect", id) != nil || !githubSandboxImageConfigValid(inspect.Bytes(), id) || ctx.Err() != nil {
		return fail()
	}
	return id, inspect.Bytes(), nil
}
