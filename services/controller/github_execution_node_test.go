package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestGitHubExecutionNode(t *testing.T) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	statement := githubTaskExecutionStatement{
		Domain:  githubTaskExecutionDomain,
		Project: "00000000-0000-4000-8000-000000000002", Task: "00000000-0000-4000-8000-000000000003",
		Revision: 4,
		Verifier: "00000000-0000-4000-8000-000000000004", Key: "00000000-0000-4000-8000-000000000005",
		Execution: githubExecutionStatement{
			Domain: githubExecutionDomain, Tenant: "00000000-0000-4000-8000-000000000001",
			Installation: 12, Repository: 34, FullName: "owner/repo",
			Branch: "feat/demo", HeadSHA: sha, ExecutedSHA: sha, Event: "push",
			Workflow: 56, Name: "CI", Path: ".github/workflows/ci.yml", SourceDigest: strings.Repeat("d", 64),
			Run: 81, Attempt: 2, Jobs: []int64{91}, Required: []string{"gate"},
		},
	}
	req := githubIsolatedRequest{statement, strings.Repeat("b", 64), strings.Repeat("c", 64)}
	wire := githubRemoteExecutionRequest{githubRemoteExecutorDomain, strings.Repeat("e", 64), statement, req.harnessDigest, req.imageDigest}
	cert := &x509.Certificate{RawSubjectPublicKeyInfo: []byte("synthetic client SPKI")}
	pin := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	state := &tls.ConnectionState{
		Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{cert},
		VerifiedChains: [][]*x509.Certificate{{cert}},
	}
	var runs atomic.Int32
	executor := githubMockIsolatedExecutor(func(ctx context.Context, r githubIsolatedRequest) (githubIsolatedReport, error) {
		runs.Add(1)
		return githubIsolatedReport{r.statement.Execution.ExecutedSHA, r.harnessDigest, r.imageDigest, []githubIsolatedJob{{91, "gate", true}}}, nil
	})
	node, err := newGitHubExecutionNode(req, pin, key, executor)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newGitHubExecutionNode(req, pin, key, nil); err == nil {
		t.Fatal("missing executor accepted")
	}
	if _, err := node.serverConfig(tls.Certificate{}, nil); err == nil {
		t.Fatal("missing TLS configuration accepted")
	}
	raw, _ := json.Marshal(wire)
	send := func(n *githubExecutionNode, body []byte, tlsState *tls.ConnectionState) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v1/execute", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.TLS = tlsState
		w := httptest.NewRecorder()
		n.ServeHTTP(w, r)
		return w
	}
	if w := send(node, raw, nil); w.Code != http.StatusForbidden {
		t.Fatal("plaintext accepted")
	}
	unverified := *state
	unverified.VerifiedChains = nil
	if w := send(node, raw, &unverified); w.Code != http.StatusForbidden {
		t.Fatal("unverified certificate accepted")
	}
	wrong := *state
	wrong.PeerCertificates = []*x509.Certificate{{RawSubjectPublicKeyInfo: []byte("wrong")}}
	if w := send(node, raw, &wrong); w.Code != http.StatusForbidden {
		t.Fatal("wrong client accepted")
	}
	if w := send(node, append(append([]byte(nil), raw...), ' '), state); w.Code != http.StatusBadRequest {
		t.Fatal("noncanonical request accepted")
	}
	bad := wire
	bad.Statement.Revision++
	changed, _ := json.Marshal(bad)
	if w := send(node, changed, state); w.Code != http.StatusForbidden {
		t.Fatal("wrong task revision accepted")
	}
	if runs.Load() != 0 {
		t.Fatal("unauthorized execution")
	}
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- send(node, raw, state)
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for w := range results {
		if w.Code == http.StatusConflict {
			continue
		}
		if w.Code != http.StatusOK {
			t.Fatal("unexpected status", w.Code)
		}
		winners++
		var signed githubRemoteSignedReport
		if err := json.Unmarshal(w.Body.Bytes(), &signed); err != nil {
			t.Fatal(err)
		}
		payload, _ := json.Marshal(signed.Report)
		sum := sha256.Sum256(raw)
		if signed.Report.RequestDigest != hex.EncodeToString(sum[:]) || !ed25519.Verify(pub, payload, signed.Signature) {
			t.Fatal("invalid signed report")
		}
	}
	if winners != 1 || runs.Load() != 1 {
		t.Fatal("duplicate execution", winners, runs.Load())
	}
	bad = wire
	bad.Nonce = strings.Repeat("f", 64)
	changed, _ = json.Marshal(bad)
	if w := send(node, changed, state); w.Code != http.StatusConflict {
		t.Fatal("second nonce bypassed one-shot authorization")
	}
	failed, err := newGitHubExecutionNode(req, pin, key, githubMockIsolatedExecutor(func(context.Context, githubIsolatedRequest) (githubIsolatedReport, error) {
		return githubIsolatedReport{}, errors.New("failure")
	}))
	if err != nil {
		t.Fatal(err)
	}
	if w := send(failed, raw, state); w.Code != http.StatusServiceUnavailable {
		t.Fatal("failed execution signed")
	}
	if w := send(failed, raw, state); w.Code != http.StatusConflict {
		t.Fatal("failed execution retried")
	}
}
