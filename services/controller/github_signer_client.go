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
	"net/url"
	"time"
)

const githubSignerTimeout = 30 * time.Second

type githubSignerClient struct {
	endpoint string
	client   *http.Client
	key      ed25519.PublicKey
}

// The endpoint, CA, TLS pin and signing key must come from protected operator
// configuration, not from the repository or the response.
func newGitHubSignerClient(endpoint string, roots *x509.CertPool, cert tls.Certificate, pin [32]byte, key ed25519.PublicKey) (*githubSignerClient, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || u.Path != "/v1/sign" ||
		roots == nil || len(cert.Certificate) == 0 || cert.PrivateKey == nil ||
		pin == ([32]byte{}) || len(key) != ed25519.PublicKeySize ||
		bytes.Equal(key, make([]byte, ed25519.PublicKeySize)) {
		return nil, errGitHubAuthorization
	}
	cfg := &tls.Config{}
	cfg.MinVersion = tls.VersionTLS13
	cfg.RootCAs = roots.Clone()
	cfg.Certificates = []tls.Certificate{cert}
	cfg.VerifyConnection = func(state tls.ConnectionState) error {
		if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 ||
			sha256.Sum256(state.PeerCertificates[0].RawSubjectPublicKeyInfo) != pin {
			return errGitHubAuthorization
		}
		return nil
	}
	transport := &http.Transport{}
	transport.TLSClientConfig = cfg
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.MaxResponseHeaderBytes = 16384
	transport.DisableKeepAlives = true
	transport.DisableCompression = true
	client := &http.Client{}
	client.Transport = transport
	client.Timeout = githubSignerTimeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &githubSignerClient{endpoint, client, append(ed25519.PublicKey(nil), key...)}, nil
}

// Reconstruct the exact task-bound statement before transmission and verify
// the returned signature against it. The observation must come from a trusted
// observer; downstream ledger writing still independently verifies freshness
// and key registration. A proof is never authorization to advance a task.
func (c *githubSignerClient) sign(ctx context.Context, policy githubIndependentPolicy, e githubCIEvidence, rel githubPRRelationship, receipt githubExecutorReceipt, signature []byte, clock func() time.Time) (githubRegisteredProof, error) {
	fail := func() (githubRegisteredProof, error) { return githubRegisteredProof{}, errGitHubAuthorization }
	if c == nil || c.client == nil || clock == nil || ctx.Err() != nil ||
		len(signature) != ed25519.SignatureSize || receipt.Domain != githubExecutorReceiptDomain ||
		!receipt.AllPassed || receipt.StartedUnix <= 0 || receipt.FinishedUnix < receipt.StartedUnix ||
		receipt.HarnessDigest != policy.harnessDigest || receipt.ImageDigest != policy.imageDigest {
		return fail()
	}
	now := clock()
	finished := time.Unix(receipt.FinishedUnix, 0)
	if finished.After(now) || now.Sub(finished) >= githubExecutorReceiptMaxAge {
		return fail()
	}
	statement, err := githubSupervisorStatement(policy, e, rel, c.key, now)
	if err != nil {
		return fail()
	}
	statement.Execution.IssuedUnix = receipt.FinishedUnix
	message, err := json.Marshal(statement)
	if err != nil || len(message) > 131072 {
		return fail()
	}
	sum := sha256.Sum256(message)
	if receipt.StatementDigest != hex.EncodeToString(sum[:]) || receipt.CheckoutSHA != statement.Execution.ExecutedSHA {
		return fail()
	}
	body, err := json.Marshal(githubSignerRequest{githubSignerRequestDomain, receipt, signature})
	if err != nil || len(body) > githubSignerBodyLimit {
		return fail()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fail()
	}
	req.GetBody = nil
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return fail()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "application/json" {
		return fail()
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, githubSignerBodyLimit+1))
	if err != nil || len(raw) > githubSignerBodyLimit || ctx.Err() != nil {
		return fail()
	}
	var wire githubSignerResponse
	if json.Unmarshal(raw, &wire) != nil {
		return fail()
	}
	canonical, err := json.Marshal(wire)
	if err != nil || !bytes.Equal(canonical, raw) || wire.Domain != githubSignerResponseDomain ||
		wire.VerifierID != policy.verifierID || wire.KeyID != policy.keyID ||
		wire.IssuedUnix != receipt.FinishedUnix || len(wire.Signature) != ed25519.SignatureSize ||
		!ed25519.Verify(c.key, message, wire.Signature) {
		return fail()
	}
	now = clock()
	if finished.After(now) || now.Sub(finished) >= githubExecutorReceiptMaxAge || !e.freshFor(policy.execution, now) || ctx.Err() != nil {
		return fail()
	}
	return githubRegisteredProof{wire.VerifierID, wire.KeyID, githubExecutionProof{wire.IssuedUnix, append([]byte(nil), wire.Signature...)}}, nil
}
