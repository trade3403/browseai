package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// A node instance authorizes exactly one execution, provisioned separately
// from the caller. No retry is allowed after admission, even on failure.
// Production provisioning must durably prevent reissuing an ambiguous grant.
type githubExecutionNode struct {
	allowed   []byte
	clientPin [32]byte
	key       ed25519.PrivateKey
	executor  githubIsolatedExecutor
	used      atomic.Bool
}

func newGitHubExecutionNode(allowed githubIsolatedRequest, clientPin [32]byte, key ed25519.PrivateKey, executor githubIsolatedExecutor) (*githubExecutionNode, error) {
	s := allowed.statement
	e := s.Execution
	if executor == nil || clientPin == ([32]byte{}) || len(key) != ed25519.PrivateKeySize ||
		!bytes.Equal(key, ed25519.NewKeyFromSeed(key.Seed())) ||
		s.Domain != githubTaskExecutionDomain || e.Domain != githubExecutionDomain ||
		!(githubEvidenceTask{s.Project, s.Task, s.Revision}).valid() ||
		!resourceUUID.MatchString(s.Verifier) || !resourceUUID.MatchString(s.Key) ||
		e.IssuedUnix != 0 || e.Run <= 0 || e.Attempt <= 0 ||
		!githubWorkflowDigestValid(e.SourceDigest) || !githubWorkflowDigestValid(allowed.harnessDigest) ||
		!githubWorkflowDigestValid(allowed.imageDigest) || len(e.Jobs) == 0 || len(e.Jobs) > 1000 {
		return nil, errGitHubAuthorization
	}
	p := githubEvidencePolicy{
		binding: githubBinding{e.Tenant, e.Installation, e.Repository, e.FullName},
		branch:  e.Branch, sha: e.HeadSHA, workflowID: e.Workflow,
		name: e.Name, path: e.Path, required: e.Required,
	}
	if !p.valid() {
		return nil, errGitHubAuthorization
	}
	for i, id := range e.Jobs {
		if id <= 0 || (i > 0 && id <= e.Jobs[i-1]) {
			return nil, errGitHubAuthorization
		}
	}
	if e.Event == "push" {
		if e.ExecutedSHA != e.HeadSHA || e.PR != 0 || e.BaseSHA != "" || e.BaseBranch != "" {
			return nil, errGitHubAuthorization
		}
	} else if e.Event == "pull_request" {
		pr := githubPRPolicy{push: p, number: e.PR, baseBranch: e.BaseBranch, baseSHA: e.BaseSHA}
		if !pr.valid() || !taskSHA.MatchString(e.ExecutedSHA) || e.ExecutedSHA == e.HeadSHA || e.ExecutedSHA == e.BaseSHA {
			return nil, errGitHubAuthorization
		}
	} else {
		return nil, errGitHubAuthorization
	}
	raw, err := json.Marshal(githubRemoteExecutionRequest{githubRemoteExecutorDomain, "", s, allowed.harnessDigest, allowed.imageDigest})
	if err != nil || len(raw) > githubRemoteBodyLimit-64 {
		return nil, errGitHubAuthorization
	}
	return &githubExecutionNode{allowed: raw, clientPin: clientPin, key: append(ed25519.PrivateKey(nil), key...), executor: executor}, nil
}

// This handler must be attached only to a dedicated TLS listener. Require
// CA-verified client certificates at the handshake AND the exact client SPKI.
func (n *githubExecutionNode) serverConfig(cert tls.Certificate, clients *x509.CertPool) (*http.Server, error) {
	if n == nil || clients == nil || len(cert.Certificate) == 0 || cert.PrivateKey == nil {
		return nil, errGitHubAuthorization
	}
	cfg := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientCAs:    clients.Clone(),
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	server := &http.Server{
		Handler: n, TLSConfig: cfg,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      githubSupervisorTimeout + 15*time.Second,
		IdleTimeout:       10 * time.Second,
		MaxHeaderBytes:    16384,
	}
	return server, nil
}

func (n *githubExecutionNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if n == nil || r.TLS == nil || r.TLS.Version < tls.VersionTLS13 ||
		len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 ||
		sha256.Sum256(r.TLS.PeerCertificates[0].RawSubjectPublicKeyInfo) != n.clientPin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost || r.URL.Path != "/v1/execute" || r.URL.RawPath != "" ||
		r.URL.RawQuery != "" || r.URL.ForceQuery || r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, githubRemoteBodyLimit))
	var wire githubRemoteExecutionRequest
	if err != nil || json.Unmarshal(body, &wire) != nil || wire.Domain != githubRemoteExecutorDomain ||
		!githubWorkflowDigestValid(wire.Nonce) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	raw, err := json.Marshal(wire)
	if err != nil || !bytes.Equal(raw, body) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	wire.Nonce = ""
	identity, err := json.Marshal(wire)
	if err != nil || !bytes.Equal(identity, n.allowed) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Context().Err() != nil || !n.used.CompareAndSwap(false, true) {
		http.Error(w, "execution unavailable", http.StatusConflict)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), githubSupervisorTimeout)
	defer cancel()
	req := githubIsolatedRequest{wire.Statement, wire.HarnessDigest, wire.ImageDigest}
	// The adapter receives no TLS or signing keys. Validate against a
	// fresh copy of the original authorization after the adapter returns.
	report, err := n.executor.execute(ctx, req)
	var original githubRemoteExecutionRequest
	if err != nil || ctx.Err() != nil || json.Unmarshal(n.allowed, &original) != nil ||
		!githubSupervisorReportValid(githubIsolatedRequest{original.Statement, original.HarnessDigest, original.ImageDigest}, report) {
		http.Error(w, "execution unavailable", http.StatusServiceUnavailable)
		return
	}
	sum := sha256.Sum256(body)
	result := githubRemoteExecutionReport{
		Domain: githubRemoteReportDomain, RequestDigest: hex.EncodeToString(sum[:]),
		CheckoutSHA: report.checkoutSHA, HarnessDigest: report.harnessDigest, ImageDigest: report.imageDigest,
	}
	for _, j := range report.jobs {
		result.Jobs = append(result.Jobs, githubRemoteJob{j.id, j.name, j.passed})
	}
	payload, err := json.Marshal(result)
	if err != nil || len(payload) > githubRemoteBodyLimit-256 || ctx.Err() != nil {
		http.Error(w, "execution unavailable", http.StatusServiceUnavailable)
		return
	}
	response, err := json.Marshal(githubRemoteSignedReport{result, ed25519.Sign(n.key, payload)})
	if err != nil || len(response) > githubRemoteBodyLimit || ctx.Err() != nil {
		http.Error(w, "execution unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(response)
}
