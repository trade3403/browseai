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
	"strings"
	"time"
)

const githubImageArchiveDomain = "browseai-image-archive-v1"
const githubImageArchiveLimit int64 = 8 << 30

type githubImageArchiveReceipt struct {
	Domain       string `json:"domain"`
	BuildDigest  string `json:"build_digest"`
	Image        string `json:"image"`
	ArchiveSHA   string `json:"archive_sha"`
	ArchiveBytes int64  `json:"archive_bytes"`
}

type githubImageArchiveProof struct {
	receipt   githubImageArchiveReceipt
	signature []byte
}

type githubImageArchiveBackend interface {
	save(context.Context, string, io.Writer) error
	load(context.Context, string, io.Reader) ([]byte, error)
}

func githubImageBuildDigest(proof githubSandboxBuildProof) (string, error) {
	raw, err := json.Marshal(proof.receipt)
	if err != nil || len(raw) > githubRemoteBodyLimit || len(proof.signature) != ed25519.SignatureSize {
		return "", errGitHubAuthorization
	}
	h := sha256.New()
	h.Write(raw)
	h.Write(proof.signature)
	return hex.EncodeToString(h.Sum(nil)), nil
}

// The caller must publish the archive only after successful signing. Partial
// output must be discarded. The trusted exporter never accepts a supplied
// archive in lieu of saving the approved immutable image.
func githubExportImage(ctx context.Context, plan githubSandboxPlan, policy githubSandboxBuildPolicy, build githubSandboxBuildProof, key ed25519.PrivateKey, out io.Writer, backend githubImageArchiveBackend) (githubImageArchiveProof, error) {
	fail := func() (githubImageArchiveProof, error) { return githubImageArchiveProof{}, errGitHubAuthorization }
	if out == nil || backend == nil || ctx.Err() != nil ||
		len(key) != ed25519.PrivateKeySize || !bytes.Equal(key, ed25519.NewKeyFromSeed(key.Seed())) ||
		!bytes.Equal(key.Public().(ed25519.PublicKey), policy.builderKey) ||
		!githubSandboxImageValid(plan.image, plan.request.imageDigest) ||
		verifyGitHubSandboxBuild(plan, policy, build, time.Now()) != nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(ctx, githubSupervisorTimeout)
	defer cancel()
	digest, err := githubImageBuildDigest(build)
	if err != nil {
		return fail()
	}
	h := sha256.New()
	w := &githubImageArchiveWriter{w: io.MultiWriter(out, h)}
	if err := backend.save(ctx, plan.image, w); err != nil || w.failed || w.n <= 0 || ctx.Err() != nil ||
		verifyGitHubSandboxBuild(plan, policy, build, time.Now()) != nil {
		return fail()
	}
	r := githubImageArchiveReceipt{githubImageArchiveDomain, digest, plan.image, hex.EncodeToString(h.Sum(nil)), w.n}
	raw, err := json.Marshal(r)
	if err != nil || ctx.Err() != nil {
		return fail()
	}
	return githubImageArchiveProof{r, ed25519.Sign(key, raw)}, nil
}

type githubImageArchiveWriter struct {
	w      io.Writer
	n      int64
	failed bool
}

func (w *githubImageArchiveWriter) Write(p []byte) (int, error) {
	if w.failed || int64(len(p)) > githubImageArchiveLimit-w.n {
		w.failed = true
		return 0, errGitHubAuthorization
	}
	n, err := w.w.Write(p)
	w.n += int64(n)
	if err != nil || n != len(p) {
		w.failed = true
		return n, errGitHubAuthorization
	}
	return n, nil
}

// Never stream unverified network bytes directly into the Docker parser.
// Copy to a protected staging file, verify all bytes, then import exactly
// that file. The staging volume must have an operator-enforced disk quota.
func githubImportImage(ctx context.Context, plan githubSandboxPlan, policy githubSandboxBuildPolicy, build githubSandboxBuildProof, archive githubImageArchiveProof, input io.Reader, staging string, backend githubImageArchiveBackend) error {
	r := archive.receipt
	if input == nil || backend == nil || ctx.Err() != nil ||
		!githubSandboxImageValid(plan.image, plan.request.imageDigest) ||
		verifyGitHubSandboxBuild(plan, policy, build, time.Now()) != nil ||
		r.Domain != githubImageArchiveDomain || r.Image != plan.image ||
		!githubWorkflowDigestValid(r.ArchiveSHA) || r.ArchiveBytes <= 0 || r.ArchiveBytes > githubImageArchiveLimit ||
		len(archive.signature) != ed25519.SignatureSize {
		return errGitHubAuthorization
	}
	digest, err := githubImageBuildDigest(build)
	if err != nil || digest != r.BuildDigest {
		return errGitHubAuthorization
	}
	raw, err := json.Marshal(r)
	if err != nil || !ed25519.Verify(policy.builderKey, raw, archive.signature) {
		return errGitHubAuthorization
	}
	if _, err := newGitHubExecutionGrantStore(staging); err != nil {
		return errGitHubAuthorization
	}
	ctx, cancel := context.WithTimeout(ctx, githubSupervisorTimeout)
	defer cancel()
	file, err := os.CreateTemp(staging, "verified-image-*.tar")
	if err != nil {
		return errGitHubAuthorization
	}
	defer os.Remove(file.Name())
	defer file.Close()
	h := sha256.New()
	reader := &githubBuildContextReader{ctx, input}
	n, err := io.Copy(io.MultiWriter(file, h), io.LimitReader(reader, r.ArchiveBytes+1))
	if err != nil || n != r.ArchiveBytes || hex.EncodeToString(h.Sum(nil)) != r.ArchiveSHA || ctx.Err() != nil {
		return errGitHubAuthorization
	}
	if err := file.Sync(); err != nil {
		return errGitHubAuthorization
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return errGitHubAuthorization
	}
	if verifyGitHubSandboxBuild(plan, policy, build, time.Now()) != nil || ctx.Err() != nil {
		return errGitHubAuthorization
	}
	inspect, err := backend.load(ctx, plan.image, file)
	if err != nil || ctx.Err() != nil || !githubSandboxImageConfigValid(inspect, plan.image) {
		return errGitHubAuthorization
	}
	return nil
}

// Separate, protected rootless daemons on dedicated VMs are required.
// The importer trusts the exporter to save only the one approved image;
// no repository-supplied tar, remote pull or tag resolution is allowed.
type githubDockerArchiveBackend struct {
	binary string
	socket string
}

func newGitHubDockerArchiveBackend(binary, socket string) (*githubDockerArchiveBackend, error) {
	if _, err := githubDockerSandboxRunner(binary, socket); err != nil {
		return nil, err
	}
	return &githubDockerArchiveBackend{binary, socket}, nil
}

func (b *githubDockerArchiveBackend) command(ctx context.Context, stdin io.Reader, stdout io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, b.binary, append([]string{"--host=unix://" + b.socket}, args...)...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "DOCKER_CONFIG=/nonexistent"}
	cmd.Dir = "/"
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, io.Discard
	cmd.WaitDelay = 5 * time.Second
	return cmd.Run()
}

func (b *githubDockerArchiveBackend) ready(ctx context.Context, image string) bool {
	if b == nil || !githubSandboxImageValid(image, strings.TrimPrefix(image, "sha256:")) {
		return false
	}
	if _, err := newGitHubDockerArchiveBackend(b.binary, b.socket); err != nil {
		return false
	}
	var info githubSandboxInspectBuffer
	return b.command(ctx, nil, &info, "info", "--format", "{{json .}}") == nil && githubSandboxRuntimeValid(info.Bytes())
}

func (b *githubDockerArchiveBackend) inspect(ctx context.Context, image string) ([]byte, error) {
	var out githubSandboxInspectBuffer
	if b.command(ctx, nil, &out, "image", "inspect", image) != nil || !githubSandboxImageConfigValid(out.Bytes(), image) {
		return nil, errGitHubAuthorization
	}
	return out.Bytes(), nil
}

func (b *githubDockerArchiveBackend) save(ctx context.Context, image string, out io.Writer) error {
	if out == nil || !b.ready(ctx, image) {
		return errGitHubAuthorization
	}
	if _, err := b.inspect(ctx, image); err != nil {
		return err
	}
	return b.command(ctx, nil, out, "image", "save", image)
}

func (b *githubDockerArchiveBackend) load(ctx context.Context, image string, input io.Reader) ([]byte, error) {
	if input == nil || !b.ready(ctx, image) {
		return nil, errGitHubAuthorization
	}
	if b.command(ctx, input, io.Discard, "image", "load", "--quiet") != nil {
		return nil, errGitHubAuthorization
	}
	return b.inspect(ctx, image)
}
