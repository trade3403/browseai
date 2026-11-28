package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGitHubBuildContext(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("offline Linux builder only")
	}
	dir := t.TempDir()
	put := func(name, data string) (string, string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256([]byte(data))
		return path, hex.EncodeToString(sum[:])
	}
	source, sourceDigest := put("source", "source archive")
	deps, depDigest := put("deps", "dependency archive")
	harness, harnessDigest := put("harness", "trusted harness")
	materials := githubBuildMaterials{source, deps, harness, "local/browseai-base"}
	policy := githubSandboxBuildPolicy{
		baseImageDigest:  strings.Repeat("a", 64),
		dependencyDigest: depDigest,
		sourceTreeDigest: sourceDigest,
	}
	var out bytes.Buffer
	ctx := context.Background()
	if err := githubWriteBuildContext(ctx, &out, materials, policy, harnessDigest); err != nil {
		t.Fatal(err)
	}
	expected := []struct {
		name, body string
		mode       int64
	}{
		{"Dockerfile", "", 0444},
		{"source.tar", "source archive", 0444},
		{"dependencies.tar", "dependency archive", 0444},
		{"trusted-test", "trusted harness", 0555},
	}
	tr := tar.NewReader(bytes.NewReader(out.Bytes()))
	for _, e := range expected {
		h, err := tr.Next()
		if err != nil || h.Name != e.name || h.Mode != e.mode || h.Typeflag != tar.TypeReg {
			t.Fatal("unexpected context entry", h, err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if e.name == "Dockerfile" {
			if !strings.Contains(string(body), "FROM local/browseai-base@sha256:"+policy.baseImageDigest) ||
				strings.Contains(string(body), "\nRUN ") || strings.Contains(string(body), "\nADD ") {
				t.Fatal("unsafe build recipe")
			}
		} else if string(body) != e.body {
			t.Fatal("wrong material bytes")
		}
	}
	if _, err := tr.Next(); err != io.EOF {
		t.Fatal("unexpected extra entry", err)
	}
	var again bytes.Buffer
	if err := githubWriteBuildContext(ctx, &again, materials, policy, harnessDigest); err != nil || !bytes.Equal(out.Bytes(), again.Bytes()) {
		t.Fatal("non-deterministic build context", err)
	}
	for _, base := range []string{"latest:tag", "base\nRUN evil", "https://base", "--flag"} {
		bad := materials
		bad.baseName = base
		if err := githubWriteBuildContext(ctx, io.Discard, bad, policy, harnessDigest); err == nil {
			t.Fatal("unapproved base accepted")
		}
	}
	badPolicy := policy
	badPolicy.sourceTreeDigest = strings.Repeat("b", 64)
	if err := githubWriteBuildContext(ctx, io.Discard, materials, badPolicy, harnessDigest); err == nil {
		t.Fatal("wrong source digest accepted")
	}
	if err := githubWriteBuildContext(ctx, io.Discard, materials, policy, strings.Repeat("c", 64)); err == nil {
		t.Fatal("wrong harness digest accepted")
	}
	if err := os.Chmod(deps, 0666); err != nil {
		t.Fatal(err)
	}
	if err := githubWriteBuildContext(ctx, io.Discard, materials, policy, harnessDigest); err == nil {
		t.Fatal("writable dependency input accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := githubWriteBuildContext(cancelled, io.Discard, materials, policy, harnessDigest); err == nil {
		t.Fatal("cancelled build accepted")
	}
}
