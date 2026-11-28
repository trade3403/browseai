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
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

const githubNodePreviewDomain = "browseai-execution-node-preview-v1"

// Only protected operator provisioning may supply this document. It is
// intentionally a local preview, not an internet-facing deployment.
type githubNodePreviewConfig struct {
	Domain           string                       `json:"domain"`
	Address          string                       `json:"address"`
	Request          githubRemoteExecutionRequest `json:"request"`
	Jobs             []githubRemoteJob            `json:"jobs"`
	BuilderID        string                       `json:"builder_id"`
	BuilderKey       []byte                       `json:"builder_key"`
	BaseDigest       string                       `json:"base_digest"`
	DependencyDigest string                       `json:"dependency_digest"`
	SourceDigest     string                       `json:"source_digest"`
	Build            githubSandboxBuildReceipt    `json:"build"`
	BuildSignature   []byte                       `json:"build_signature"`
	Archive          githubImageArchiveReceipt    `json:"archive"`
	ArchiveSignature []byte                       `json:"archive_signature"`
	ClientPin        string                       `json:"client_pin"`
	DockerBinary     string                       `json:"docker_binary"`
	DockerSocket     string                       `json:"docker_socket"`
	Staging          string                       `json:"staging"`
	Grants           string                       `json:"grants"`
}

func githubNodePreviewAddressValid(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host != "127.0.0.1" {
		return false
	}
	p, err := strconv.ParseUint(port, 10, 16)
	return err == nil && p > 0 && strconv.FormatUint(p, 10) == port
}

func githubDecodeNodePreview(raw []byte) (githubNodePreviewConfig, error) {
	var c githubNodePreviewConfig
	if len(raw) == 0 || len(raw) > githubRemoteBodyLimit || json.Unmarshal(raw, &c) != nil {
		return githubNodePreviewConfig{}, errGitHubAuthorization
	}
	// Require the exact canonical object, including all fields; unknown or
	// duplicate fields, alternate encodings and trailing data are rejected.
	check, err := json.Marshal(c)
	if err != nil || !bytes.Equal(check, raw) || c.Domain != githubNodePreviewDomain ||
		!githubNodePreviewAddressValid(c.Address) || c.Request.Domain != githubRemoteExecutorDomain ||
		c.Request.Nonce != "" || c.Staging == c.Grants ||
		!githubStrictEd25519Key(c.BuilderKey) || !githubWorkflowDigestValid(c.ClientPin) {
		return githubNodePreviewConfig{}, errGitHubAuthorization
	}
	return c, nil
}

// Use only fixed filenames in the protected directory. Operators must keep
// the directory and all parent paths outside workload control.
func githubOpenNodePreviewFile(dir, name string, limit int64) (*os.File, error) {
	if _, err := newGitHubExecutionGrantStore(dir); err != nil {
		return nil, err
	}
	if filepath.Base(name) != name || name == "." || name == ".." {
		return nil, errGitHubAuthorization
	}
	path := filepath.Join(dir, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() <= 0 || info.Size() > limit {
		return nil, errGitHubAuthorization
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errGitHubAuthorization
	}
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) || opened.Size() != info.Size() || opened.Mode().Perm() != 0600 {
		f.Close()
		return nil, errGitHubAuthorization
	}
	return f, nil
}

func githubReadNodePreviewFile(dir, name string, limit int64) ([]byte, error) {
	f, err := githubOpenNodePreviewFile(dir, name, limit)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, errGitHubAuthorization
	}
	return raw, nil
}

func githubPrepareNodePreview(ctx context.Context, c githubNodePreviewConfig, cert tls.Certificate, roots *x509.CertPool, key ed25519.PrivateKey, input io.Reader, backend githubImageArchiveBackend, runner func(context.Context, string, []string) error) (*http.Server, error) {
	if !githubNodePreviewAddressValid(c.Address) || c.Domain != githubNodePreviewDomain ||
		c.Request.Domain != githubRemoteExecutorDomain || c.Request.Nonce != "" ||
		len(key) != ed25519.PrivateKeySize || bytes.Equal(key.Public().(ed25519.PublicKey), c.BuilderKey) ||
		c.Staging == c.Grants || ctx.Err() != nil {
		return nil, errGitHubAuthorization
	}
	pin, err := hex.DecodeString(c.ClientPin)
	if err != nil || len(pin) != 32 || !githubWorkflowDigestValid(c.ClientPin) {
		return nil, errGitHubAuthorization
	}
	req := githubIsolatedRequest{c.Request.Statement, c.Request.HarnessDigest, c.Request.ImageDigest}
	plan := githubSandboxPlan{request: req, image: "sha256:" + req.imageDigest}
	for _, j := range c.Jobs {
		plan.jobs = append(plan.jobs, githubIsolatedJob{j.ID, j.Name, j.Passed})
	}
	policy := githubSandboxBuildPolicy{c.BuilderID, ed25519.PublicKey(c.BuilderKey), c.BaseDigest, c.DependencyDigest, c.SourceDigest}
	build := githubSandboxBuildProof{c.Build, c.BuildSignature}
	archive := githubImageArchiveProof{c.Archive, c.ArchiveSignature}
	grants, err := newGitHubExecutionGrantStore(c.Grants)
	if err != nil {
		return nil, err
	}
	executor, err := newGitHubSandboxExecutor(plan, policy, build, runner)
	if err != nil {
		return nil, err
	}
	node, err := newGitHubExecutionNode(req, [32]byte(pin), key, executor, grants)
	if err != nil {
		return nil, err
	}
	server, err := node.serverConfig(cert, roots)
	if err != nil {
		return nil, err
	}
	if err := githubImportImage(ctx, plan, policy, build, archive, input, c.Staging, backend); err != nil || ctx.Err() != nil {
		return nil, errGitHubAuthorization
	}
	server.Addr = c.Address
	server.BaseContext = func(net.Listener) context.Context { return ctx }
	return server, nil
}

func githubExecutionNodePreviewCommand(dir string) error {
	// The preview is explicit and refuses root or non-Linux operation.
	if runtime.GOOS != "linux" || os.Geteuid() <= 0 {
		return errGitHubAuthorization
	}
	raw, err := githubReadNodePreviewFile(dir, "node.json", githubRemoteBodyLimit)
	if err != nil {
		return err
	}
	c, err := githubDecodeNodePreview(raw)
	if err != nil {
		return err
	}
	seed, err := githubReadNodePreviewFile(dir, "node.seed", ed25519.SeedSize)
	if err != nil || len(seed) != ed25519.SeedSize {
		return errGitHubAuthorization
	}
	key := ed25519.NewKeyFromSeed(seed)
	clear(seed)
	certPEM, err := githubReadNodePreviewFile(dir, "server.pem", 65536)
	if err != nil {
		return err
	}
	keyPEM, err := githubReadNodePreviewFile(dir, "server.key", 65536)
	if err != nil {
		return err
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	clear(keyPEM)
	if err != nil {
		return errGitHubAuthorization
	}
	ca, err := githubReadNodePreviewFile(dir, "client-ca.pem", 65536)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return errGitHubAuthorization
	}
	input, err := githubOpenNodePreviewFile(dir, "image.tar", githubImageArchiveLimit)
	if err != nil {
		return err
	}
	defer input.Close()
	backend, err := newGitHubDockerArchiveBackend(c.DockerBinary, c.DockerSocket)
	if err != nil {
		return err
	}
	runner, err := githubDockerSandboxRunner(c.DockerBinary, c.DockerSocket)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server, err := githubPrepareNodePreview(ctx, c, cert, roots, key, input, backend, runner)
	if err != nil {
		return err
	}
	input.Close()
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
