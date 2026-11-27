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
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type githubPreviewTestArchive struct {
	t     *testing.T
	calls int
}

func (b *githubPreviewTestArchive) save(context.Context, string, io.Writer) error {
	b.t.Fatal("unexpected archive export")
	return errGitHubAuthorization
}

func (b *githubPreviewTestArchive) load(_ context.Context, image string, input io.Reader) ([]byte, error) {
	b.calls++
	raw, err := io.ReadAll(input)
	if err != nil || string(raw) != "synthetic image archive" {
		b.t.Fatal("unverified archive reached importer", err)
	}
	return []byte(`[{"Id":"` + image + `","Os":"linux","Config":{"User":"65532:65532","Env":["PATH=/usr/bin:/bin","HOME=/tmp"],"Entrypoint":["/opt/browseai/trusted-test"],"Cmd":null,"WorkingDir":"/","Volumes":null,"OnBuild":null}}]`), nil
}

func githubPreviewFixture(t *testing.T) (githubNodePreviewConfig, ed25519.PrivateKey, ed25519.PrivateKey, []byte) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("persistent node requires Linux")
	}
	dir := func() string {
		t.Helper()
		d := t.TempDir()
		if err := os.Chmod(d, 0700); err != nil {
			t.Fatal(err)
		}
		return d
	}
	pub, builder, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, nodeKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	var c githubNodePreviewConfig
	c.Domain = githubNodePreviewDomain
	c.Address = "127.0.0.1:8443"
	c.BuilderID = "00000000-0000-4000-8000-000000000006"
	c.BuilderKey = pub
	c.BaseDigest = strings.Repeat("d", 64)
	c.DependencyDigest = strings.Repeat("e", 64)
	c.SourceDigest = strings.Repeat("f", 64)
	c.Staging = dir()
	c.Grants = dir()
	c.Request.Domain = githubRemoteExecutorDomain
	c.Request.HarnessDigest = strings.Repeat("b", 64)
	c.Request.ImageDigest = strings.Repeat("c", 64)
	s := &c.Request.Statement
	s.Domain = githubTaskExecutionDomain
	s.Project = "00000000-0000-4000-8000-000000000002"
	s.Task = "00000000-0000-4000-8000-000000000003"
	s.Revision = 4
	s.Verifier = "00000000-0000-4000-8000-000000000004"
	s.Key = "00000000-0000-4000-8000-000000000005"
	e := &s.Execution
	e.Domain = githubExecutionDomain
	e.Tenant = "00000000-0000-4000-8000-000000000001"
	e.Installation = 12
	e.Repository = 34
	e.FullName = "owner/repo"
	e.Branch = "feat/demo"
	e.HeadSHA = strings.Repeat("a", 40)
	e.ExecutedSHA = e.HeadSHA
	e.Event = "push"
	e.Workflow = 56
	e.Name = "CI"
	e.Path = ".github/workflows/ci.yml"
	e.SourceDigest = strings.Repeat("d", 64)
	e.Run = 81
	e.Attempt = 2
	e.Jobs = []int64{91}
	e.Required = []string{"gate"}
	c.Jobs = []githubRemoteJob{{91, "gate", true}}
	request, err := githubSandboxRequestBytes(githubIsolatedRequest{*s, c.Request.HarnessDigest, c.Request.ImageDigest})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(request)
	c.Build.Domain = githubSandboxBuildDomain
	c.Build.Builder = c.BuilderID
	c.Build.RequestDigest = hex.EncodeToString(sum[:])
	c.Build.CheckoutSHA = e.ExecutedSHA
	c.Build.SourceTreeDigest = c.SourceDigest
	c.Build.HarnessDigest = c.Request.HarnessDigest
	c.Build.BaseImageDigest = c.BaseDigest
	c.Build.DependencyDigest = c.DependencyDigest
	c.Build.ImageDigest = c.Request.ImageDigest
	c.Build.Jobs = c.Jobs
	c.Build.BuiltUnix = time.Now().Unix()
	sign := func(v any) []byte {
		t.Helper()
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return ed25519.Sign(builder, raw)
	}
	c.BuildSignature = sign(c.Build)
	digest, err := githubImageBuildDigest(githubSandboxBuildProof{c.Build, c.BuildSignature})
	if err != nil {
		t.Fatal(err)
	}
	archive := []byte("synthetic image archive")
	sum = sha256.Sum256(archive)
	c.Archive = githubImageArchiveReceipt{githubImageArchiveDomain, digest, "sha256:" + c.Request.ImageDigest, hex.EncodeToString(sum[:]), int64(len(archive))}
	c.ArchiveSignature = sign(c.Archive)
	return c, builder, nodeKey, archive
}

func TestGitHubNodePreviewPreparation(t *testing.T) {
	c, builder, nodeKey, archive := githubPreviewFixture(t)
	fixture := httptest.NewTLSServer(http.NotFoundHandler())
	cert := fixture.TLS.Certificates[0]
	roots := x509.NewCertPool()
	roots.AddCert(fixture.Certificate())
	pin := sha256.Sum256(fixture.Certificate().RawSubjectPublicKeyInfo)
	fixture.Close()

	c.ClientPin = hex.EncodeToString(pin[:])
	backend := &githubPreviewTestArchive{t: t}
	runner := func(context.Context, string, []string) error {
		t.Fatal("startup must not execute a workload")
		return errGitHubAuthorization
	}
	ctx := context.Background()
	reject := func(name string, cfg githubNodePreviewConfig, key ed25519.PrivateKey, certificate tls.Certificate, ca *x509.CertPool, data []byte, callCtx context.Context) {
		t.Helper()
		server, err := githubPrepareNodePreview(callCtx, cfg, certificate, ca, key, bytes.NewReader(data), backend, runner)
		if err == nil || server != nil || backend.calls != 0 {
			t.Fatal("unsafe startup accepted or reached importer", name, err)
		}
	}
	reject("shared signing key", c, builder, cert, roots, archive, ctx)
	bad := c
	bad.ClientPin = "invalid"
	reject("invalid client pin", bad, nodeKey, cert, roots, archive, ctx)
	bad = c
	bad.Grants = filepath.Join(c.Grants, "missing")
	reject("missing grant directory", bad, nodeKey, cert, roots, archive, ctx)
	bad = c
	bad.BuildSignature = make([]byte, ed25519.SignatureSize)
	reject("invalid build proof", bad, nodeKey, cert, roots, archive, ctx)
	bad = c
	bad.Request.Statement.Task = "invalid"
	// Re-sign the altered build so this reaches node identity validation.
	request, err := githubSandboxRequestBytes(githubIsolatedRequest{bad.Request.Statement, bad.Request.HarnessDigest, bad.Request.ImageDigest})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(request)
	bad.Build.RequestDigest = hex.EncodeToString(sum[:])
	raw, err := json.Marshal(bad.Build)
	if err != nil {
		t.Fatal(err)
	}
	bad.BuildSignature = ed25519.Sign(builder, raw)
	reject("invalid node identity", bad, nodeKey, cert, roots, archive, ctx)
	reject("missing certificate", c, nodeKey, tls.Certificate{}, roots, archive, ctx)
	reject("missing CA", c, nodeKey, cert, nil, archive, ctx)
	reject("tampered archive", c, nodeKey, cert, roots, []byte("tampered"), ctx)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	reject("cancelled startup", c, nodeKey, cert, roots, archive, cancelled)

	server, err := githubPrepareNodePreview(ctx, c, cert, roots, nodeKey, bytes.NewReader(archive), backend, runner)
	if err != nil || server == nil || backend.calls != 1 {
		t.Fatal("valid preview preparation failed", err)
	}
	if server.Addr != c.Address || server.TLSConfig.MinVersion != tls.VersionTLS13 ||
		server.TLSConfig.ClientAuth != tls.RequireAndVerifyClientCert || server.BaseContext(nil) != ctx {
		t.Fatal("unsafe server configuration")
	}
	if _, ok := server.Handler.(*githubExecutionNode); !ok {
		t.Fatal("incorrect node handler")
	}
	if files, err := os.ReadDir(c.Staging); err != nil || len(files) != 0 {
		t.Fatal("staging file not cleaned up", err)
	}
}
