package main

import (
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
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestGitHubRemoteExecutor(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	req := githubIsolatedRequest{
		statement: githubTaskExecutionStatement{
			Domain: githubTaskExecutionDomain,
			Execution: githubExecutionStatement{
				ExecutedSHA: strings.Repeat("a", 40), Jobs: []int64{91}, Required: []string{"gate"},
			},
		},
		harnessDigest: strings.Repeat("b", 64), imageDigest: strings.Repeat("c", 64),
	}
	var mu sync.Mutex
	mode := ""
	var previous []byte
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodPost || r.URL.Path != "/v1/execute" || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "bad request", 400)
			return
		}
		raw, err := io.ReadAll(r.Body)
		var wire githubRemoteExecutionRequest
		if err != nil || json.Unmarshal(raw, &wire) != nil || wire.Domain != githubRemoteExecutorDomain || len(wire.Nonce) != 64 {
			http.Error(w, "bad request", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		sum := sha256.Sum256(raw)
		report := githubRemoteExecutionReport{
			Domain: githubRemoteReportDomain, RequestDigest: hex.EncodeToString(sum[:]),
			CheckoutSHA: wire.Statement.Execution.ExecutedSHA, HarnessDigest: wire.HarnessDigest, ImageDigest: wire.ImageDigest,
			Jobs: []githubRemoteJob{{91, "gate", true}},
		}
		switch mode {
		case "redirect":
			http.Redirect(w, r, "/other", http.StatusTemporaryRedirect)
			return
		case "unavailable":
			w.WriteHeader(503)
			return
		case "replay":
			w.Write(previous)
			return
		case "digest":
			report.RequestDigest = strings.Repeat("a", 64)
		case "checkout":
			report.CheckoutSHA = strings.Repeat("b", 40)
		case "failed":
			report.Jobs[0].Passed = false
		case "oversize":
			w.Write([]byte(strings.Repeat("x", githubRemoteBodyLimit+1)))
			return
		case "type":
			w.Header().Set("Content-Type", "text/plain")
		}
		payload, _ := json.Marshal(report)
		signed := githubRemoteSignedReport{report, ed25519.Sign(priv, payload)}
		if mode == "signature" {
			signed.Signature[0] ^= 1
		}
		body, _ := json.Marshal(signed)
		if mode == "noncanonical" {
			body = append(body, ' ')
		}
		if mode == "" {
			previous = append([]byte(nil), body...)
		}
		w.Write(body)
	}))
	// This disposable server requires a client certificate. The fixture
	// accepts any certificate; production nodes must verify the client CA
	// and authorized identity, not use this test TLS setting.
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	pin := sha256.Sum256(server.Certificate().RawSubjectPublicKeyInfo)
	cert := server.TLS.Certificates[0]
	endpoint := server.URL + "/v1/execute"
	x, err := newGitHubRemoteExecutor(endpoint, roots, cert, pin, pub)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if report, err := x.execute(ctx, req); err != nil || !githubSupervisorReportValid(req, report) {
		t.Fatal("valid authenticated report rejected", err)
	}
	for _, bad := range []string{"redirect", "unavailable", "replay", "digest", "checkout", "failed", "oversize", "type", "signature", "noncanonical"} {
		mu.Lock()
		mode = bad
		mu.Unlock()
		if _, err := x.execute(ctx, req); err == nil {
			t.Fatal("invalid report accepted", bad)
		}
	}
	mu.Lock()
	mode = ""
	mu.Unlock()
	wrongPin := pin
	wrongPin[0] ^= 1
	wrong, err := newGitHubRemoteExecutor(endpoint, roots, cert, wrongPin, pub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrong.execute(ctx, req); err == nil {
		t.Fatal("wrong TLS pin accepted")
	}
	for _, bad := range []string{"http://localhost/v1/execute", server.URL + "/other", endpoint + "?x=1", endpoint + "#x"} {
		if _, err := newGitHubRemoteExecutor(bad, roots, cert, pin, pub); err == nil {
			t.Fatal("bad endpoint accepted", bad)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := x.execute(cancelled, req); err == nil {
		t.Fatal("cancelled request accepted")
	}
}
