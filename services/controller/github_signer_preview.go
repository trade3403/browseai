package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

const githubSignerPreviewDomain = "browseai-independent-signer-preview-v1"

// The operator provisions a single immutable policy in a protected directory.
// No request or repository controls these fields.
type githubSignerPreviewConfig struct {
	Domain        string   `json:"domain"`
	Address       string   `json:"address"`
	AppIssuer     string   `json:"app_issuer"`
	ClientPin     string   `json:"client_pin"`
	SupervisorKey []byte   `json:"supervisor_key"`
	Tenant        string   `json:"tenant"`
	Installation  int64    `json:"installation"`
	Repository    int64    `json:"repository"`
	FullName      string   `json:"full_name"`
	Branch        string   `json:"branch"`
	HeadSHA       string   `json:"head_sha"`
	Workflow      int64    `json:"workflow"`
	Name          string   `json:"name"`
	Path          string   `json:"path"`
	Required      []string `json:"required"`
	PR            int64    `json:"pr"`
	BaseBranch    string   `json:"base_branch"`
	BaseSHA       string   `json:"base_sha"`
	Project       string   `json:"project"`
	Task          string   `json:"task"`
	Revision      int64    `json:"revision"`
	Verifier      string   `json:"verifier"`
	Key           string   `json:"key"`
	SourceDigest  string   `json:"source_digest"`
	HarnessDigest string   `json:"harness_digest"`
	ImageDigest   string   `json:"image_digest"`
}

func (c githubSignerPreviewConfig) policy() (githubIndependentPolicy, error) {
	p := githubIndependentPolicy{}
	p.execution = githubEvidencePolicy{githubBinding{c.Tenant, c.Installation, c.Repository, c.FullName}, c.Branch, c.HeadSHA, c.Workflow, c.Name, c.Path, append([]string(nil), c.Required...)}
	p.task = githubEvidenceTask{c.Project, c.Task, c.Revision}
	p.verifierID, p.keyID = c.Verifier, c.Key
	p.sourceDigest, p.harnessDigest, p.imageDigest = c.SourceDigest, c.HarnessDigest, c.ImageDigest
	if c.Domain != githubSignerPreviewDomain || !githubNodePreviewAddressValid(c.Address) ||
		!githubAppIssuer.MatchString(c.AppIssuer) || !githubWorkflowDigestValid(c.ClientPin) ||
		c.ClientPin == hex.EncodeToString(make([]byte, 32)) ||
		len(c.SupervisorKey) != ed25519.PublicKeySize || bytes.Equal(c.SupervisorKey, make([]byte, ed25519.PublicKeySize)) ||
		!p.execution.valid() || !p.task.valid() || !resourceUUID.MatchString(p.verifierID) || !resourceUUID.MatchString(p.keyID) ||
		!githubWorkflowDigestValid(p.sourceDigest) || !githubWorkflowDigestValid(p.harnessDigest) || !githubWorkflowDigestValid(p.imageDigest) {
		return githubIndependentPolicy{}, errGitHubAuthorization
	}
	if c.PR == 0 {
		if c.BaseBranch != "" || c.BaseSHA != "" {
			return githubIndependentPolicy{}, errGitHubAuthorization
		}
	} else {
		pr := githubPRPolicy{p.execution, c.PR, c.BaseBranch, c.BaseSHA}
		if !pr.valid() {
			return githubIndependentPolicy{}, errGitHubAuthorization
		}
		p.pr = &pr
	}
	return p, nil
}

func githubDecodeSignerPreview(raw []byte) (githubSignerPreviewConfig, error) {
	var c githubSignerPreviewConfig
	if len(raw) == 0 || len(raw) > githubRemoteBodyLimit || json.Unmarshal(raw, &c) != nil {
		return githubSignerPreviewConfig{}, errGitHubAuthorization
	}
	canonical, err := json.Marshal(c)
	if err != nil || !bytes.Equal(canonical, raw) {
		return githubSignerPreviewConfig{}, errGitHubAuthorization
	}
	if _, err := c.policy(); err != nil {
		return githubSignerPreviewConfig{}, err
	}
	return c, nil
}

func githubPrepareSignerPreview(ctx context.Context, dir string, clock func() time.Time) (*http.Server, error) {
	if ctx.Err() != nil || clock == nil {
		return nil, errGitHubAuthorization
	}
	raw, err := githubReadNodePreviewFile(dir, "signer.json", githubRemoteBodyLimit)
	if err != nil {
		return nil, err
	}
	c, err := githubDecodeSignerPreview(raw)
	if err != nil {
		return nil, err
	}
	p, err := c.policy()
	if err != nil {
		return nil, err
	}
	seed, err := githubReadNodePreviewFile(dir, "signer.seed", ed25519.SeedSize)
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, errGitHubAuthorization
	}
	key := ed25519.NewKeyFromSeed(seed)
	clear(seed)
	defer clear(key)
	// Require the same protected-directory checks for the App key before
	// invoking the existing PEM loader. All parents must be operator-owned.
	f, err := githubOpenNodePreviewFile(dir, "app.pem", 65536)
	if err != nil {
		return nil, err
	}
	f.Close()
	appKey, err := loadGitHubAppKey(filepath.Join(dir, "app.pem"))
	if err != nil {
		return nil, err
	}
	// Do not use an environment-configured HTTP proxy for signer credentials.
	transport := &http.Transport{}
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.MaxResponseHeaderBytes = 16384
	app, err := newGitHubApp(c.AppIssuer, appKey, transport)
	if err != nil {
		return nil, err
	}
	observe, err := newGitHubTrustedSignerObserver(app, p, clock)
	if err != nil {
		return nil, err
	}
	pin, err := hex.DecodeString(c.ClientPin)
	if err != nil || len(pin) != 32 {
		return nil, errGitHubAuthorization
	}
	service, err := newGitHubSignerService(githubIndependentSigner{key, ed25519.PublicKey(c.SupervisorKey)}, p, [32]byte(pin), observe, clock)
	if err != nil {
		return nil, err
	}
	certPEM, err := githubReadNodePreviewFile(dir, "server.pem", 65536)
	if err != nil {
		return nil, err
	}
	keyPEM, err := githubReadNodePreviewFile(dir, "server.key", 65536)
	if err != nil {
		return nil, err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	clear(keyPEM)
	if err != nil {
		return nil, errGitHubAuthorization
	}
	ca, err := githubReadNodePreviewFile(dir, "client-ca.pem", 65536)
	if err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) || ctx.Err() != nil {
		return nil, errGitHubAuthorization
	}
	cfg := &tls.Config{}
	cfg.MinVersion = tls.VersionTLS13
	cfg.Certificates = []tls.Certificate{cert}
	cfg.ClientCAs = roots
	cfg.ClientAuth = tls.RequireAndVerifyClientCert
	server := &http.Server{}
	server.Addr, server.TLSConfig = c.Address, cfg
	server.ReadHeaderTimeout = 5 * time.Second
	server.ReadTimeout = 10 * time.Second
	server.WriteTimeout = githubEvidenceLifetime + 10*time.Second
	server.IdleTimeout = 10 * time.Second
	server.MaxHeaderBytes = 16384
	server.BaseContext = func(net.Listener) context.Context { return ctx }
	// Bound simultaneous observations for the single protected policy.
	slot := make(chan struct{}, 1)
	server.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case slot <- struct{}{}:
			defer func() { <-slot }()
			service.ServeHTTP(w, r)
		default:
			http.Error(w, "signing unavailable", http.StatusServiceUnavailable)
		}
	})
	return server, nil
}

func githubSignerPreviewCommand(dir string) error {
	if runtime.GOOS != "linux" || os.Geteuid() <= 0 {
		return errGitHubAuthorization
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server, err := githubPrepareSignerPreview(ctx, dir, time.Now)
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServeTLS("", "") }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errGitHubAuthorization
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if err := server.Shutdown(shutdown); err != nil {
			server.Close()
			return errGitHubAuthorization
		}
		return nil
	}
}
