package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

const githubSignerRequestDomain = "browseai-independent-signer-request-v1"
const githubSignerResponseDomain = "browseai-independent-signer-response-v1"
const githubSignerBodyLimit = 8192

type githubSignerRequest struct {
	Domain    string                `json:"domain"`
	Receipt   githubExecutorReceipt `json:"receipt"`
	Signature []byte                `json:"signature"`
}

type githubSignerResponse struct {
	Domain     string `json:"domain"`
	VerifierID string `json:"verifier_id"`
	KeyID      string `json:"key_id"`
	IssuedUnix int64  `json:"issued_unix"`
	Signature  []byte `json:"signature"`
}

// This handler is a single-policy signing boundary, not a public API.
// Its observer must independently fetch and validate GitHub evidence and
// workflow source. Neither policy nor observation may come from the caller.
type githubSignerService struct {
	signer    githubIndependentSigner
	policy    githubIndependentPolicy
	clientPin [32]byte
	observe   githubSupervisorObserve
	clock     func() time.Time
}

func newGitHubSignerService(signer githubIndependentSigner, policy githubIndependentPolicy, pin [32]byte, observe githubSupervisorObserve, clock func() time.Time) (*githubSignerService, error) {
	if len(signer.privateKey) != ed25519.PrivateKeySize || len(signer.supervisorKey) != ed25519.PublicKeySize ||
		!bytes.Equal(signer.privateKey, ed25519.NewKeyFromSeed(signer.privateKey.Seed())) ||
		bytes.Equal(signer.privateKey.Public().(ed25519.PublicKey), signer.supervisorKey) ||
		pin == ([32]byte{}) || observe == nil || clock == nil ||
		!policy.task.valid() || !policy.execution.valid() ||
		!resourceUUID.MatchString(policy.verifierID) || !resourceUUID.MatchString(policy.keyID) ||
		!githubWorkflowDigestValid(policy.sourceDigest) || !githubWorkflowDigestValid(policy.harnessDigest) ||
		!githubWorkflowDigestValid(policy.imageDigest) {
		return nil, errGitHubAuthorization
	}
	policy.execution.required = append([]string(nil), policy.execution.required...)
	if policy.pr != nil {
		p := *policy.pr
		if !p.valid() {
			return nil, errGitHubAuthorization
		}
		p.push.required = append([]string(nil), p.push.required...)
		policy.pr = &p
	}
	signer.privateKey = append(ed25519.PrivateKey(nil), signer.privateKey...)
	signer.supervisorKey = append(ed25519.PublicKey(nil), signer.supervisorKey...)
	return &githubSignerService{signer, policy, pin, observe, clock}, nil
}

func (s *githubSignerService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s == nil || r.TLS == nil || r.TLS.Version < tls.VersionTLS13 ||
		len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 ||
		sha256.Sum256(r.TLS.PeerCertificates[0].RawSubjectPublicKeyInfo) != s.clientPin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/v1/sign" || r.URL.RawPath != "" ||
		r.URL.RawQuery != "" || r.URL.ForceQuery || r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, githubSignerBodyLimit))
	var req githubSignerRequest
	if err != nil || json.Unmarshal(body, &req) != nil || req.Domain != githubSignerRequestDomain {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	canonical, err := json.Marshal(req)
	if err != nil || !bytes.Equal(canonical, body) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	// Authenticate the receipt before spending a GitHub API observation.
	receipt, err := json.Marshal(req.Receipt)
	now := s.clock()
	finished := time.Unix(req.Receipt.FinishedUnix, 0)
	if err != nil || len(req.Signature) != ed25519.SignatureSize ||
		!ed25519.Verify(s.signer.supervisorKey, receipt, req.Signature) ||
		req.Receipt.Domain != githubExecutorReceiptDomain || !req.Receipt.AllPassed ||
		req.Receipt.StartedUnix <= 0 || req.Receipt.FinishedUnix < req.Receipt.StartedUnix ||
		finished.After(now) || now.Sub(finished) >= githubExecutorReceiptMaxAge || r.Context().Err() != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	e, rel, err := s.observe(r.Context())
	if err != nil || r.Context().Err() != nil {
		http.Error(w, "signing unavailable", http.StatusServiceUnavailable)
		return
	}
	proof, err := s.signer.sign(s.policy, e, rel, req.Receipt, req.Signature, s.clock())
	if err != nil || r.Context().Err() != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	// Signing the same valid receipt again returns the same deterministic
	// proof; this endpoint neither consumes evidence nor advances tasks.
	raw, err := json.Marshal(githubSignerResponse{githubSignerResponseDomain, proof.verifierID, proof.keyID, proof.proof.issuedUnix, proof.proof.signature})
	if err != nil {
		http.Error(w, "signing unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}
