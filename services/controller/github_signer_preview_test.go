package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestGitHubSignerPreview(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("protected Linux configuration only")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	supervisor, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, signer, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := githubSignerPreviewConfig{}
	c.Domain = githubSignerPreviewDomain
	c.Address = "127.0.0.1:8444"
	c.AppIssuer = "mock-app"
	c.ClientPin = strings.Repeat("a", 64)
	c.SupervisorKey = supervisor
	c.Tenant = "00000000-0000-4000-8000-000000000001"
	c.Installation, c.Repository = 12, 34
	c.FullName, c.Branch = "owner/repo", "feat/demo"
	c.HeadSHA = strings.Repeat("a", 40)
	c.Workflow = 56
	c.Name, c.Path = "CI", ".github/workflows/ci.yml"
	c.Required = []string{"gate"}
	c.Project = "00000000-0000-4000-8000-000000000002"
	c.Task = "00000000-0000-4000-8000-000000000003"
	c.Revision = 4
	c.Verifier = "00000000-0000-4000-8000-000000000004"
	c.Key = "00000000-0000-4000-8000-000000000005"
	c.SourceDigest = strings.Repeat("d", 64)
	c.HarnessDigest = strings.Repeat("e", 64)
	c.ImageDigest = strings.Repeat("f", 64)
	encode := func(c githubSignerPreviewConfig) []byte {
		t.Helper()
		raw, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	raw := encode(c)
	if _, err := githubDecodeSignerPreview(raw); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*githubSignerPreviewConfig){
		func(c *githubSignerPreviewConfig) { c.Address = "0.0.0.0:8444" },
		func(c *githubSignerPreviewConfig) { c.ClientPin = strings.Repeat("0", 64) },
		func(c *githubSignerPreviewConfig) { c.SupervisorKey = make([]byte, 32) },
		func(c *githubSignerPreviewConfig) { c.BaseBranch = "main" },
		func(c *githubSignerPreviewConfig) { c.PR = -1 },
		func(c *githubSignerPreviewConfig) { c.SourceDigest = "invalid" },
	} {
		bad := c
		mutate(&bad)
		if _, err := githubDecodeSignerPreview(encode(bad)); err == nil {
			t.Fatal("unsafe configuration accepted")
		}
	}
	if _, err := githubDecodeSignerPreview(append(append([]byte(nil), raw...), ' ')); err == nil {
		t.Fatal("noncanonical configuration accepted")
	}
	pr := c
	pr.PR, pr.BaseBranch, pr.BaseSHA = 3, "main", strings.Repeat("b", 40)
	if p, err := pr.policy(); err != nil || p.pr == nil {
		t.Fatal("valid PR policy rejected", err)
	}
	write := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("signer.json", raw)
	write("signer.seed", signer.Seed())
	app, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	write("app.pem", pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(app)}))
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	cert := fixture.TLS.Certificates[0]
	fixture.Close()
	pk, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	write("server.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}))
	write("server.key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}))
	write("client-ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]}))
	ctx := context.Background()
	server, err := githubPrepareSignerPreview(ctx, dir, time.Now)
	if err != nil || server == nil {
		t.Fatal("protected preparation failed", err)
	}
	if server.Addr != c.Address || server.TLSConfig.MinVersion != tls.VersionTLS13 ||
		server.TLSConfig.ClientAuth != tls.RequireAndVerifyClientCert || server.BaseContext(nil) != ctx {
		t.Fatal("unsafe server configuration")
	}
	w := httptest.NewRecorder()
	server.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/sign", nil))
	if w.Code != http.StatusForbidden {
		t.Fatal("plaintext signing accepted")
	}
	write("signer.seed", make([]byte, 31))
	if _, err := githubPrepareSignerPreview(ctx, dir, time.Now); err == nil {
		t.Fatal("short seed accepted")
	}
	write("signer.seed", signer.Seed())
	bad := c
	bad.SupervisorKey = signer.Public().(ed25519.PublicKey)
	write("signer.json", encode(bad))
	if _, err := githubPrepareSignerPreview(ctx, dir, time.Now); err == nil {
		t.Fatal("shared signing identity accepted")
	}
	write("signer.json", raw)
	if err := os.Chmod(filepath.Join(dir, "app.pem"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := githubPrepareSignerPreview(ctx, dir, time.Now); err == nil {
		t.Fatal("public App key accepted")
	}
	if err := os.Chmod(filepath.Join(dir, "app.pem"), 0600); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := githubPrepareSignerPreview(cancelled, dir, time.Now); err == nil {
		t.Fatal("cancelled startup accepted")
	}
}
