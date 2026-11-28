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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestGitHubIndependentSigner(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	supervisor, supervisorPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer := githubIndependentSigner{private, supervisor}
	now := time.Unix(1800000000, 0)
	p := githubEvidencePolicy{
		binding: githubBinding{"00000000-0000-4000-8000-000000000001", 12, 34, "owner/repo"},
		branch:  "feat/demo", sha: strings.Repeat("a", 40), workflowID: 56,
		name: "CI", path: ".github/workflows/ci.yml", required: []string{"gate"},
	}
	task := githubEvidenceTask{"00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000003", 4}
	e := githubCIEvidence{p, 81, 2, []int64{91, 92}, now}
	policy := githubIndependentPolicy{
		task: task, execution: p, sourceDigest: strings.Repeat("d", 64),
		harnessDigest: strings.Repeat("e", 64), imageDigest: strings.Repeat("f", 64),
		verifierID: "00000000-0000-4000-8000-000000000004",
		keyID:      "00000000-0000-4000-8000-000000000005",
	}
	makeReceipt := func(policy githubIndependentPolicy, rel githubPRRelationship) githubExecutorReceipt {
		t.Helper()
		proof := githubRegisteredProof{verifierID: policy.verifierID, keyID: policy.keyID, proof: githubExecutionProof{issuedUnix: now.Unix()}}
		statement := githubTaskExecutionMessage(task, p, e, policy.sourceDigest, policy.pr, rel, proof)
		raw, err := json.Marshal(statement)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		return githubExecutorReceipt{
			Domain: githubExecutorReceiptDomain, StatementDigest: hex.EncodeToString(sum[:]),
			CheckoutSHA: statement.Execution.ExecutedSHA, HarnessDigest: policy.harnessDigest, ImageDigest: policy.imageDigest,
			StartedUnix: now.Add(-time.Minute).Unix(), FinishedUnix: now.Unix(), AllPassed: true,
		}
	}
	signReceipt := func(receipt githubExecutorReceipt) []byte {
		t.Helper()
		raw, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		return ed25519.Sign(supervisorPrivate, raw)
	}
	rel := githubPRRelationship{}
	receipt := makeReceipt(policy, rel)
	sig := signReceipt(receipt)
	proof, err := signer.sign(policy, e, rel, receipt, sig, now)
	if err != nil {
		t.Fatal("trusted execution rejected", err)
	}
	if _, err := verifyGitHubTaskExecution(task, p, e, policy.sourceDigest, nil, rel, proof, public, now); err != nil {
		t.Fatal("signed proof incompatible with controller", err)
	}

	t.Run("signing-service", func(t *testing.T) {
		cert := &x509.Certificate{RawSubjectPublicKeyInfo: []byte("synthetic supervisor certificate")}
		pin := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
		state := &tls.ConnectionState{}
		state.Version = tls.VersionTLS13
		state.PeerCertificates = []*x509.Certificate{cert}
		state.VerifiedChains = [][]*x509.Certificate{{cert}}
		calls := 0
		failObserve := false
		changed := false
		observe := func(context.Context) (githubCIEvidence, githubPRRelationship, error) {
			calls++
			if failObserve {
				return githubCIEvidence{}, githubPRRelationship{}, errGitHubAuthorization
			}
			current := e
			if changed {
				current.attempt++
			}
			return current, rel, nil
		}
		service, err := newGitHubSignerService(signer, policy, pin, observe, func() time.Time { return now })
		if err != nil {
			t.Fatal(err)
		}
		if _, err := newGitHubSignerService(signer, policy, [32]byte{}, observe, func() time.Time { return now }); err == nil {
			t.Fatal("missing client pin accepted")
		}
		wire := githubSignerRequest{githubSignerRequestDomain, receipt, sig}
		raw, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		send := func(path string, body []byte, tlsState *tls.ConnectionState) *httptest.ResponseRecorder {
			r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			r.TLS = tlsState
			w := httptest.NewRecorder()
			service.ServeHTTP(w, r)
			return w
		}
		if w := send("/v1/sign", raw, nil); w.Code != http.StatusForbidden {
			t.Fatal("plaintext signing accepted")
		}
		other := *state
		other.PeerCertificates = []*x509.Certificate{{RawSubjectPublicKeyInfo: []byte("wrong peer")}}
		if w := send("/v1/sign", raw, &other); w.Code != http.StatusForbidden {
			t.Fatal("wrong client accepted")
		}
		if w := send("/v1/sign?x=1", raw, state); w.Code != http.StatusBadRequest {
			t.Fatal("query accepted")
		}
		if w := send("/v1/sign", append(append([]byte(nil), raw...), ' '), state); w.Code != http.StatusBadRequest {
			t.Fatal("noncanonical request accepted")
		}
		bad := wire
		bad.Signature = make([]byte, ed25519.SignatureSize)
		body, _ := json.Marshal(bad)
		if w := send("/v1/sign", body, state); w.Code != http.StatusForbidden || calls != 0 {
			t.Fatal("unauthenticated receipt reached observer")
		}
		failObserve = true
		if w := send("/v1/sign", raw, state); w.Code != http.StatusServiceUnavailable {
			t.Fatal("failed observation signed")
		}
		failObserve = false
		changed = true
		if w := send("/v1/sign", raw, state); w.Code != http.StatusForbidden {
			t.Fatal("changed attempt signed")
		}
		changed = false
		w := send("/v1/sign", raw, state)
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("valid signing request rejected", w.Code)
		}
		var response githubSignerResponse
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Domain != githubSignerResponseDomain || response.VerifierID != policy.verifierID || response.KeyID != policy.keyID {
			t.Fatal("incorrect proof identity")
		}
		got := githubRegisteredProof{response.VerifierID, response.KeyID, githubExecutionProof{response.IssuedUnix, response.Signature}}
		if _, err := verifyGitHubTaskExecution(task, p, e, policy.sourceDigest, nil, rel, got, public, now); err != nil {
			t.Fatal("service proof failed controller verification", err)
		}
		if replay := send("/v1/sign", raw, state); replay.Code != http.StatusOK || replay.Body.String() != w.Body.String() {
			t.Fatal("non-deterministic proof")
		}
	})
	for _, mutate := range []func(*githubExecutorReceipt){
		func(r *githubExecutorReceipt) { r.Domain = "other" },
		func(r *githubExecutorReceipt) { r.AllPassed = false },
		func(r *githubExecutorReceipt) { r.CheckoutSHA = strings.Repeat("b", 40) },
		func(r *githubExecutorReceipt) { r.StatementDigest = strings.Repeat("a", 64) },
		func(r *githubExecutorReceipt) { r.HarnessDigest = strings.Repeat("a", 64) },
		func(r *githubExecutorReceipt) { r.ImageDigest = strings.Repeat("a", 64) },
		func(r *githubExecutorReceipt) { r.FinishedUnix++ },
		func(r *githubExecutorReceipt) { r.FinishedUnix -= 301; r.StartedUnix -= 301 },
		func(r *githubExecutorReceipt) { r.StartedUnix = r.FinishedUnix + 1 },
	} {
		bad := receipt
		mutate(&bad)
		// Even an authentic supervisor receipt cannot override pinned policy.
		if _, err := signer.sign(policy, e, rel, bad, signReceipt(bad), now); err == nil {
			t.Fatal("invalid execution accepted")
		}
	}
	if _, err := signer.sign(policy, e, rel, receipt, make([]byte, 64), now); err == nil {
		t.Fatal("unsigned receipt accepted")
	}
	if _, err := (githubIndependentSigner{private, public}).sign(policy, e, rel, receipt, sig, now); err == nil {
		t.Fatal("same signer and supervisor accepted")
	}
	if _, err := (githubIndependentSigner{}).sign(policy, e, rel, receipt, sig, now); err == nil {
		t.Fatal("missing keys accepted")
	}
	for _, mutate := range []func(*githubIndependentPolicy){
		func(p *githubIndependentPolicy) { p.task.revision++ },
		func(p *githubIndependentPolicy) { p.task.taskID = p.task.projectID },
		func(p *githubIndependentPolicy) { p.keyID = p.verifierID },
		func(p *githubIndependentPolicy) { p.sourceDigest = strings.Repeat("a", 64) },
		func(p *githubIndependentPolicy) { p.execution.sha = strings.Repeat("b", 40) },
	} {
		bad := policy
		mutate(&bad)
		if _, err := signer.sign(bad, e, rel, receipt, sig, now); err == nil {
			t.Fatal("changed task policy accepted")
		}
	}
	if _, err := signer.sign(policy, e, rel, receipt, sig, now.Add(githubEvidenceLifetime)); err == nil {
		t.Fatal("stale observation accepted")
	}
	pr := githubPRPolicy{push: p, number: 3, baseBranch: "main", baseSHA: strings.Repeat("b", 40)}
	rel = githubPRRelationship{3, p.sha, pr.baseSHA, strings.Repeat("c", 40)}
	policy.pr = &pr
	receipt = makeReceipt(policy, rel)
	proof, err = signer.sign(policy, e, rel, receipt, signReceipt(receipt), now)
	if err != nil {
		t.Fatal("PR execution rejected", err)
	}
	if _, err := verifyGitHubTaskExecution(task, p, e, policy.sourceDigest, &pr, rel, proof, public, now); err != nil {
		t.Fatal("PR proof incompatible with controller", err)
	}
	receipt.CheckoutSHA = p.sha
	if _, err := signer.sign(policy, e, rel, receipt, signReceipt(receipt), now); err == nil {
		t.Fatal("PR head checkout accepted instead of merge")
	}
}
