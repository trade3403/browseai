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
	"io"
	"net/http"
	"net/url"
	"time"
)

const githubRemoteExecutorDomain = "browseai-isolated-executor-request-v1"
const githubRemoteReportDomain = "browseai-isolated-executor-report-v1"
const githubRemoteBodyLimit = 262144

type githubRemoteExecutionRequest struct {
	Domain        string                       `json:"domain"`
	Nonce         string                       `json:"nonce"`
	Statement     githubTaskExecutionStatement `json:"statement"`
	HarnessDigest string                       `json:"harness_digest"`
	ImageDigest   string                       `json:"image_digest"`
}

type githubRemoteJob struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
}

type githubRemoteExecutionReport struct {
	Domain        string            `json:"domain"`
	RequestDigest string            `json:"request_digest"`
	CheckoutSHA   string            `json:"checkout_sha"`
	HarnessDigest string            `json:"harness_digest"`
	ImageDigest   string            `json:"image_digest"`
	Jobs          []githubRemoteJob `json:"jobs"`
}

type githubRemoteSignedReport struct {
	Report    githubRemoteExecutionReport `json:"report"`
	Signature []byte                      `json:"signature"`
}

type githubRemoteExecutor struct {
	endpoint string
	client   *http.Client
	nodeKey  ed25519.PublicKey
}

// Provision all arguments from protected operator configuration. No user or
// repository-controlled URLs, certificates, keys or TLS settings are accepted.
// A dedicated node must protect its report key outside the tested workload.
func newGitHubRemoteExecutor(endpoint string, roots *x509.CertPool, clientCert tls.Certificate, serverPin [32]byte, nodeKey ed25519.PublicKey) (*githubRemoteExecutor, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" ||
		u.Fragment != "" || u.ForceQuery || u.Path != "/v1/execute" || u.RawPath != "" ||
		roots == nil || len(clientCert.Certificate) == 0 || clientCert.PrivateKey == nil ||
		serverPin == ([32]byte{}) || len(nodeKey) != ed25519.PublicKeySize {
		return nil, errGitHubAuthorization
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13, RootCAs: roots.Clone(),
			Certificates: []tls.Certificate{clientCert},
			VerifyConnection: func(state tls.ConnectionState) error {
				if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 ||
					sha256.Sum256(state.PeerCertificates[0].RawSubjectPublicKeyInfo) != serverPin {
					return errGitHubAuthorization
				}
				return nil
			},
		},
		TLSHandshakeTimeout:    10 * time.Second,
		MaxResponseHeaderBytes: 16384,
		DisableKeepAlives:      true,
		DisableCompression:     true,
	}
	client := &http.Client{
		Transport: transport, Timeout: githubSupervisorTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	return &githubRemoteExecutor{endpoint, client, append(ed25519.PublicKey(nil), nodeKey...)}, nil
}

// Never retry automatically after ambiguous execution. Each request has a
// random nonce; a report for a previous request cannot be accepted.
func (x *githubRemoteExecutor) execute(ctx context.Context, req githubIsolatedRequest) (githubIsolatedReport, error) {
	fail := func() (githubIsolatedReport, error) { return githubIsolatedReport{}, errGitHubAuthorization }
	if x == nil || x.client == nil || ctx.Err() != nil || req.statement.Domain != githubTaskExecutionDomain ||
		req.statement.Execution.IssuedUnix != 0 || !githubWorkflowDigestValid(req.harnessDigest) ||
		!githubWorkflowDigestValid(req.imageDigest) {
		return fail()
	}
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fail()
	}
	wire := githubRemoteExecutionRequest{githubRemoteExecutorDomain, hex.EncodeToString(nonce[:]), req.statement, req.harnessDigest, req.imageDigest}
	raw, err := json.Marshal(wire)
	if err != nil || len(raw) > githubRemoteBodyLimit {
		return fail()
	}
	sum := sha256.Sum256(raw)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, x.endpoint, bytes.NewReader(raw))
	if err != nil {
		return fail()
	}
	// Suppress automatic replay of the request body by the transport.
	httpReq.GetBody = nil
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	resp, err := x.client.Do(httpReq)
	if err != nil {
		return fail()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" {
		return fail()
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, githubRemoteBodyLimit+1))
	if err != nil || len(body) > githubRemoteBodyLimit || ctx.Err() != nil {
		return fail()
	}
	var signed githubRemoteSignedReport
	if err := json.Unmarshal(body, &signed); err != nil {
		return fail()
	}
	// Require exact canonical JSON: reject unknown/duplicate fields and
	// ambiguous encodings rather than accepting a partially parsed report.
	canonical, err := json.Marshal(signed)
	if err != nil || !bytes.Equal(canonical, body) || len(signed.Signature) != ed25519.SignatureSize {
		return fail()
	}
	r := signed.Report
	payload, err := json.Marshal(r)
	if err != nil || r.Domain != githubRemoteReportDomain || r.RequestDigest != hex.EncodeToString(sum[:]) ||
		!ed25519.Verify(x.nodeKey, payload, signed.Signature) || len(r.Jobs) > 1000 {
		return fail()
	}
	report := githubIsolatedReport{checkoutSHA: r.CheckoutSHA, harnessDigest: r.HarnessDigest, imageDigest: r.ImageDigest}
	for _, j := range r.Jobs {
		report.jobs = append(report.jobs, githubIsolatedJob{j.ID, j.Name, j.Passed})
	}
	if !githubSupervisorReportValid(req, report) || ctx.Err() != nil {
		return fail()
	}
	return report, nil
}
