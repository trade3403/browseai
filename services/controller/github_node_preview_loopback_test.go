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
	"math/big"
	"net"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

// All certificates and keys are generated in memory for this test only.
func githubPreviewTestPKI(t *testing.T) (*x509.CertPool, tls.Certificate, tls.Certificate, tls.Certificate, [32]byte, [32]byte) {
	t.Helper()
	caPub, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	ca := &x509.Certificate{}
	ca.SerialNumber = big.NewInt(1)
	ca.NotBefore, ca.NotAfter = now.Add(-time.Hour), now.Add(time.Hour)
	ca.IsCA = true
	ca.BasicConstraintsValid = true
	ca.KeyUsage = x509.KeyUsageCertSign
	raw, err := x509.CreateCertificate(rand.Reader, ca, ca, caPub, caKey)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parent)
	leaf := func(serial int64, usage x509.ExtKeyUsage) (tls.Certificate, [32]byte) {
		t.Helper()
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		template := &x509.Certificate{}
		template.SerialNumber = big.NewInt(serial)
		template.NotBefore, template.NotAfter = now.Add(-time.Hour), now.Add(time.Hour)
		template.KeyUsage = x509.KeyUsageDigitalSignature
		template.ExtKeyUsage = []x509.ExtKeyUsage{usage}
		template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		raw, err := x509.CreateCertificate(rand.Reader, template, parent, pub, caKey)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := x509.ParseCertificate(raw)
		if err != nil {
			t.Fatal(err)
		}
		return tls.Certificate{Certificate: [][]byte{raw}, PrivateKey: key, Leaf: parsed}, sha256.Sum256(parsed.RawSubjectPublicKeyInfo)
	}
	server, serverPin := leaf(2, x509.ExtKeyUsageServerAuth)
	client, clientPin := leaf(3, x509.ExtKeyUsageClientAuth)
	other, _ := leaf(4, x509.ExtKeyUsageClientAuth)
	return roots, server, client, other, serverPin, clientPin
}

func TestGitHubNodePreviewLoopback(t *testing.T) {
	c, _, nodeKey, archive := githubPreviewFixture(t)
	roots, cert, clientCert, otherCert, serverPin, clientPin := githubPreviewTestPKI(t)
	c.ClientPin = hex.EncodeToString(clientPin[:])
	var runs atomic.Int32
	runner := func(_ context.Context, name string, args []string) error {
		runs.Add(1)
		if !slices.Equal(args, githubSandboxArgs("sha256:"+c.Request.ImageDigest, name)) {
			return errGitHubAuthorization
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := func() *httptest.Server {
		t.Helper()
		backend := &githubPreviewTestArchive{t: t}
		server, err := githubPrepareNodePreview(ctx, c, cert, roots, nodeKey, bytes.NewReader(archive), backend, runner)
		if err != nil || backend.calls != 1 {
			t.Fatal("preview preparation failed", err)
		}
		// Start the production handler and its TLS configuration on an
		// ephemeral loopback port; only the Docker boundary is mocked.
		ts := httptest.NewUnstartedServer(server.Handler)
		ts.TLS = server.TLSConfig
		ts.StartTLS()
		t.Cleanup(ts.Close)
		return ts
	}
	server := start()
	req := githubIsolatedRequest{c.Request.Statement, c.Request.HarnessDigest, c.Request.ImageDigest}
	nodePub := nodeKey.Public().(ed25519.PublicKey)
	remote := func(ts *httptest.Server, client tls.Certificate, pin [32]byte, key ed25519.PublicKey) *githubRemoteExecutor {
		t.Helper()
		x, err := newGitHubRemoteExecutor(ts.URL+"/v1/execute", roots, client, pin, key)
		if err != nil {
			t.Fatal(err)
		}
		return x
	}
	wrongPin := serverPin
	wrongPin[0] ^= 1
	if _, err := remote(server, clientCert, wrongPin, nodePub).execute(ctx, req); err == nil {
		t.Fatal("wrong server pin accepted")
	}
	if _, err := remote(server, otherCert, serverPin, nodePub).execute(ctx, req); err == nil {
		t.Fatal("CA-valid but unauthorized client accepted")
	}
	x := remote(server, clientCert, serverPin, nodePub)
	bad := req
	bad.statement.Revision++
	if _, err := x.execute(ctx, bad); err == nil || runs.Load() != 0 {
		t.Fatal("unauthorized request reached execution")
	}
	report, err := x.execute(ctx, req)
	if err != nil || !githubSupervisorReportValid(req, report) || runs.Load() != 1 {
		t.Fatal("authenticated execution or signed report failed", err)
	}
	if _, err := x.execute(ctx, req); err == nil || runs.Load() != 1 {
		t.Fatal("second nonce replayed execution")
	}
	server.Close()
	restarted := start()
	if _, err := remote(restarted, clientCert, serverPin, nodePub).execute(ctx, req); err == nil || runs.Load() != 1 {
		t.Fatal("node reconstruction replayed durable grant")
	}
}
