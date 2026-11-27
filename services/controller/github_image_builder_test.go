package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type githubMockImageBuilder func(context.Context, string, io.Reader) (string, []byte, error)

func (f githubMockImageBuilder) build(ctx context.Context, base string, r io.Reader) (string, []byte, error) {
	return f(ctx, base, r)
}

func TestGitHubBuildSandboxImage(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("offline Linux builder only")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	put := func(name string) (string, string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(name))
		return path, hex.EncodeToString(sum[:])
	}
	source, sourceDigest := put("source")
	deps, depsDigest := put("dependencies")
	harness, harnessDigest := put("harness")
	pub, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	policy := githubSandboxBuildPolicy{
		builderID: "00000000-0000-4000-8000-000000000001", builderKey: pub,
		baseImageDigest: strings.Repeat("a", 64), dependencyDigest: depsDigest,
		sourceTreeDigest: sourceDigest,
	}
	plan := githubSandboxPlan{
		request: githubIsolatedRequest{
			statement: githubTaskExecutionStatement{
				Domain: githubTaskExecutionDomain,
				Execution: githubExecutionStatement{
					ExecutedSHA: strings.Repeat("b", 40), Jobs: []int64{91}, Required: []string{"gate"},
				},
			},
			harnessDigest: harnessDigest,
		},
		jobs: []githubIsolatedJob{{91, "gate", true}},
	}
	materials := githubBuildMaterials{source, deps, harness, "local/base"}
	id := "sha256:" + strings.Repeat("c", 64)
	base := "local/base@sha256:" + policy.baseImageDigest
	inspect := `[{"Id":"` + id + `","Os":"linux","Config":{"User":"65532:65532","Env":["PATH=/usr/bin:/bin","HOME=/tmp"],"Entrypoint":["/opt/browseai/trusted-test"],"WorkingDir":"/"}}]`
	mode := ""
	calls := 0
	backend := githubMockImageBuilder(func(ctx context.Context, ref string, r io.Reader) (string, []byte, error) {
		calls++
		if ref != base {
			t.Fatal("wrong base")
		}
		if n, err := io.Copy(io.Discard, r); err != nil || n == 0 {
			t.Fatal("missing context", err)
		}
		switch mode {
		case "failed":
			return "", nil, errors.New("build failed")
		case "bad config":
			return id, []byte(strings.Replace(inspect, "65532:65532", "0", 1)), nil
		case "bad id":
			return "latest", []byte(inspect), nil
		}
		return id, []byte(inspect), nil
	})
	ctx := context.Background()
	built, proof, err := githubBuildSandboxImage(ctx, plan, materials, policy, private, dir, backend)
	if err != nil || built.image != id || verifyGitHubSandboxBuild(built, policy, proof, time.Now()) != nil {
		t.Fatal("valid build rejected", err)
	}
	t.Run("archive", func(t *testing.T) {
		var output bytes.Buffer
		archiveBackend := &githubMockArchiveBackend{inspect: []byte(inspect)}
		archive, err := githubExportImage(ctx, built, policy, proof, private, &output, archiveBackend)
		if err != nil || output.String() != "image archive" {
			t.Fatal("export failed", err)
		}
		importImage := func(a githubImageArchiveProof, data string) error {
			return githubImportImage(ctx, built, policy, proof, a, strings.NewReader(data), dir, archiveBackend)
		}
		if err := importImage(archive, output.String()); err != nil || archiveBackend.loads != 1 {
			t.Fatal("import failed", err)
		}
		for _, data := range []string{"image archiv", "image archive!", "image archivE"} {
			if err := importImage(archive, data); err == nil || archiveBackend.loads != 1 {
				t.Fatal("invalid bytes reached importer")
			}
		}
		for _, mutate := range []func(*githubImageArchiveReceipt){
			func(r *githubImageArchiveReceipt) { r.BuildDigest = strings.Repeat("d", 64) },
			func(r *githubImageArchiveReceipt) { r.Image = "sha256:" + strings.Repeat("d", 64) },
			func(r *githubImageArchiveReceipt) { r.ArchiveBytes = githubImageArchiveLimit + 1 },
			func(r *githubImageArchiveReceipt) { r.Domain = "other" },
		} {
			bad := archive.receipt
			mutate(&bad)
			raw, err := json.Marshal(bad)
			if err != nil {
				t.Fatal(err)
			}
			if err := importImage(githubImageArchiveProof{bad, ed25519.Sign(private, raw)}, output.String()); err == nil || archiveBackend.loads != 1 {
				t.Fatal("invalid manifest reached importer")
			}
		}
		bad := archive
		bad.signature = make([]byte, ed25519.SignatureSize)
		if err := importImage(bad, output.String()); err == nil || archiveBackend.loads != 1 {
			t.Fatal("invalid signature reached importer")
		}
		archiveBackend.inspect = []byte(strings.Replace(inspect, "65532:65532", "0", 1))
		if err := importImage(archive, output.String()); err == nil || archiveBackend.loads != 2 {
			t.Fatal("unsafe imported image accepted")
		}
		archiveBackend.fail = true
		if _, err := githubExportImage(ctx, built, policy, proof, private, io.Discard, archiveBackend); err == nil {
			t.Fatal("failed export signed")
		}
		w := &githubImageArchiveWriter{w: io.Discard, n: githubImageArchiveLimit}
		if _, err := w.Write([]byte("x")); err == nil {
			t.Fatal("oversized archive accepted")
		}
		if _, err := newGitHubDockerArchiveBackend("docker", "/run/docker.sock"); err == nil {
			t.Fatal("relative runtime accepted")
		}
	})
	for _, bad := range []string{"failed", "bad config", "bad id"} {
		mode = bad
		if _, p, err := githubBuildSandboxImage(ctx, plan, materials, policy, private, dir, backend); err == nil || len(p.signature) != 0 {
			t.Fatal("invalid build signed", bad)
		}
	}
	bad := policy
	bad.sourceTreeDigest = strings.Repeat("d", 64)
	if _, _, err := githubBuildSandboxImage(ctx, plan, materials, bad, private, dir, backend); err == nil || calls != 4 {
		t.Fatal("invalid material reached builder")
	}
	if _, _, err := githubBuildSandboxImage(ctx, built, materials, policy, private, dir, backend); err == nil || calls != 4 {
		t.Fatal("caller-supplied output ID accepted")
	}
	baseInfo := `[{"Os":"linux","RepoDigests":["` + base + `"],"Config":{"OnBuild":null,"Volumes":null}}]`
	if !githubBuildBaseValid([]byte(baseInfo), base) {
		t.Fatal("valid base rejected")
	}
	for _, raw := range []string{
		strings.Replace(baseInfo, base, "other", 1),
		strings.Replace(baseInfo, `"OnBuild":null`, `"OnBuild":["RUN evil"]`, 1),
		strings.Replace(baseInfo, `"Volumes":null`, `"Volumes":{"/data":{}}`, 1),
	} {
		if githubBuildBaseValid([]byte(raw), base) {
			t.Fatal("unsafe base accepted")
		}
	}
	if _, err := newGitHubDockerImageBuilder("docker", "/run/docker.sock", dir); err == nil {
		t.Fatal("relative runtime accepted")
	}
}

type githubMockArchiveBackend struct {
	inspect []byte
	loads   int
	fail    bool
}

func (b *githubMockArchiveBackend) save(ctx context.Context, image string, out io.Writer) error {
	if b.fail {
		return errors.New("save failed")
	}
	_, err := io.WriteString(out, "image archive")
	return err
}

func (b *githubMockArchiveBackend) load(ctx context.Context, image string, input io.Reader) ([]byte, error) {
	b.loads++
	raw, err := io.ReadAll(input)
	if err != nil || string(raw) != "image archive" {
		return nil, errors.New("wrong archive")
	}
	return b.inspect, nil
}
