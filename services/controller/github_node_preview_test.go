package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGitHubNodePreviewConfiguration(t *testing.T) {
	builder, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := githubNodePreviewConfig{
		Domain: githubNodePreviewDomain, Address: "127.0.0.1:8443",
		Request:    githubRemoteExecutionRequest{Domain: githubRemoteExecutorDomain},
		BuilderKey: builder, ClientPin: strings.Repeat("a", 64),
		Staging: "/staging", Grants: "/grants",
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := githubDecodeNodePreview(raw); err != nil {
		t.Fatal("valid configuration rejected", err)
	}
	weak := c
	weak.BuilderKey = make([]byte, 32)
	weakRaw, err := json.Marshal(weak)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := githubDecodeNodePreview(weakRaw); err == nil {
		t.Fatal("weak builder key accepted")
	}
	for _, addr := range []string{"0.0.0.0:8443", "[::]:8443", "localhost:8443", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:08443"} {
		bad := c
		bad.Address = addr
		raw, _ := json.Marshal(bad)
		if _, err := githubDecodeNodePreview(raw); err == nil {
			t.Fatal("unsafe listener accepted", addr)
		}
	}
	for _, bad := range [][]byte{append(append([]byte(nil), raw...), ' '), []byte("{}"), nil} {
		if _, err := githubDecodeNodePreview(bad); err == nil {
			t.Fatal("noncanonical configuration accepted")
		}
	}
	if runtime.GOOS != "linux" {
		return
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "node.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := githubReadNodePreviewFile(dir, "node.json", githubRemoteBodyLimit); err != nil || string(got) != string(raw) {
		t.Fatal("private configuration unreadable", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := githubReadNodePreviewFile(dir, "node.json", githubRemoteBodyLimit); err == nil {
		t.Fatal("public configuration accepted")
	}
	if _, err := githubOpenNodePreviewFile(dir, "../node.json", githubRemoteBodyLimit); err == nil {
		t.Fatal("path traversal accepted")
	}
	if err := os.Symlink(path, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := githubOpenNodePreviewFile(dir, "link", githubRemoteBodyLimit); err == nil {
		t.Fatal("symlink accepted")
	}
}
