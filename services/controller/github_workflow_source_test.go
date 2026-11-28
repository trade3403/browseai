package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestGitHubWorkflowSource(t *testing.T) {
	const content = "name: CI\non: push\n"
	sum := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(sum[:])
	path := ".github/workflows/ci.yml"
	source := githubWorkflowSource{Type: "file", Path: path, Size: len(content), Encoding: "base64", Content: base64.StdEncoding.EncodeToString([]byte(content))}
	if err := verifyGitHubWorkflowSource(path, digest, source); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*githubWorkflowSource){
		func(s *githubWorkflowSource) { s.Type = "symlink" },
		func(s *githubWorkflowSource) { s.Path = "other.yml" },
		func(s *githubWorkflowSource) { s.Target = "other.yml" },
		func(s *githubWorkflowSource) { s.Encoding = "none" },
		func(s *githubWorkflowSource) { s.Size++ },
		func(s *githubWorkflowSource) { s.Size = githubWorkflowSourceLimit + 1 },
		func(s *githubWorkflowSource) { s.Content = "invalid" },
		func(s *githubWorkflowSource) {
			s.Content = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", len(content))))
		},
	} {
		bad := source
		change(&bad)
		if err := verifyGitHubWorkflowSource(path, digest, bad); err == nil {
			t.Fatal("invalid source accepted")
		}
	}
	for _, bad := range []string{"", strings.ToUpper(digest), strings.Repeat("0", 64)} {
		if err := verifyGitHubWorkflowSource(path, bad, source); err == nil {
			t.Fatal("invalid digest accepted")
		}
	}
}

func TestGitHubRemoteWorkflowSource(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	p := githubEvidencePolicy{
		binding: githubBinding{"00000000-0000-4000-8000-000000000001", 123, 456, "owner/repo"},
		branch:  "feat/demo", sha: strings.Repeat("a", 40), workflowID: 56,
		name: "CI", path: ".github/workflows/ci.yml", required: []string{"gate"},
	}
	pr := githubPRPolicy{push: p, number: 3, baseBranch: "main", baseSHA: strings.Repeat("b", 40)}
	relation := githubPRRelationship{3, p.sha, pr.baseSHA, strings.Repeat("c", 40)}
	const content = "name: CI\non: push\n"
	sum := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(sum[:])
	var refs []string
	changed := ""
	repoBody := `{"id":456,"full_name":"owner/repo"}`
	a, err := newGitHubApp("mock-app", key, githubTestTransport(func(r *http.Request) (*http.Response, error) {
		body := ""
		status := http.StatusOK
		switch r.URL.Path {
		case "/app/installations/123/access_tokens":
			status = http.StatusCreated
			body = `{"token":"synthetic-only-token","expires_at":"2026-11-27T01:00:00Z","repositories":[{"id":456}],"permissions":{"actions":"read","checks":"read","contents":"read","pull_requests":"read","metadata":"read"}}`
		case "/repos/owner/repo":
			body = repoBody
		case "/repos/owner/repo/contents/.github/workflows/ci.yml":
			ref := r.URL.Query().Get("ref")
			refs = append(refs, ref)
			text := content
			if ref == changed {
				text = "modified workflow"
			}
			raw, err := json.Marshal(githubWorkflowSource{Type: "file", Path: p.path, Size: len(text), Encoding: "base64", Content: base64.StdEncoding.EncodeToString([]byte(text))})
			if err != nil {
				t.Fatal(err)
			}
			body = string(raw)
		default:
			t.Fatal("unexpected request", r.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := a.verifyPushWorkflowSource(ctx, p, digest, now); err != nil || len(refs) != 1 || refs[0] != p.sha {
		t.Fatal("push source rejected", err)
	}
	refs = nil
	if err := a.verifyPRWorkflowSource(ctx, pr, relation, digest, now); err != nil || len(refs) != 3 || refs[1] != pr.baseSHA || refs[2] != relation.mergeSHA {
		t.Fatal("PR source rejected", err)
	}
	for _, revision := range []string{p.sha, pr.baseSHA, relation.mergeSHA} {
		changed = revision
		if err := a.verifyPRWorkflowSource(ctx, pr, relation, digest, now); err == nil {
			t.Fatal("modified workflow accepted")
		}
	}
	changed = ""
	refs = nil
	for _, revisions := range [][]string{nil, {p.sha, p.sha}, {pr.baseSHA}, {"main"}, {p.sha, p.sha, p.sha, p.sha}} {
		if err := a.verifyWorkflowSource(ctx, p, digest, revisions, now); err == nil || len(refs) != 0 {
			t.Fatal("invalid revision accepted")
		}
	}
	repoBody = `{"id":457,"full_name":"owner/repo"}`
	if err := a.verifyPushWorkflowSource(ctx, p, digest, now); err == nil || len(refs) != 0 {
		t.Fatal("foreign repository accepted")
	}
}
