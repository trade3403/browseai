package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

// Exercise the entire protocol chain through two independently authenticated
// TLS listeners. GitHub observations and the Docker boundary are synthetic;
// this test does not establish real-host isolation or authorize task movement.
func TestGitHubExecutionTrustChain(t *testing.T) {
	c, _, nodeKey, archive := githubPreviewFixture(t)
	s := c.Request.Statement
	e := s.Execution
	p := githubEvidencePolicy{githubBinding{e.Tenant, e.Installation, e.Repository, e.FullName}, e.Branch, e.HeadSHA, e.Workflow, e.Name, e.Path, e.Required}
	policy := githubIndependentPolicy{}
	policy.task = githubEvidenceTask{s.Project, s.Task, s.Revision}
	policy.execution = p
	policy.sourceDigest = e.SourceDigest
	policy.harnessDigest = c.Request.HarnessDigest
	policy.imageDigest = c.Request.ImageDigest
	policy.verifierID, policy.keyID = s.Verifier, s.Key
	now := time.Now().UTC().Truncate(time.Second)
	clock := func() time.Time { return now }
	var changed atomic.Bool
	var observations atomic.Int32
	observe := func(ctx context.Context) (githubCIEvidence, githubPRRelationship, error) {
		observations.Add(1)
		ev := githubCIEvidence{p, e.Run, e.Attempt, append([]int64(nil), e.Jobs...), now}
		if changed.Load() {
			ev.attempt++
		}
		return ev, githubPRRelationship{}, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	nodeRoots, nodeCert, nodeClient, _, nodePin, nodeClientPin := githubPreviewTestPKI(t)
	c.ClientPin = hex.EncodeToString(nodeClientPin[:])
	var runs atomic.Int32
	runner := func(_ context.Context, name string, args []string) error {
		runs.Add(1)
		if !slices.Equal(args, githubSandboxArgs("sha256:"+c.Request.ImageDigest, name)) {
			return errGitHubAuthorization
		}
		return nil
	}
	node, err := githubPrepareNodePreview(ctx, c, nodeCert, nodeRoots, nodeKey, bytes.NewReader(archive), &githubPreviewTestArchive{t: t}, runner)
	if err != nil {
		t.Fatal(err)
	}
	nodeServer := httptest.NewUnstartedServer(node.Handler)
	nodeServer.TLS = node.TLSConfig
	nodeServer.StartTLS()
	defer nodeServer.Close()
	executor, err := newGitHubRemoteExecutor(nodeServer.URL+"/v1/execute", nodeRoots, nodeClient, nodePin, nodeKey.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	supervisorPub, supervisorKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signerPub, signerKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	supervisor := githubSupervisor{supervisorKey, executor}
	receipt, signature, err := supervisor.run(ctx, policy, observe, clock)
	if err != nil || runs.Load() != 1 || observations.Load() != 2 {
		t.Fatal("supervisor failed", err)
	}
	if _, _, err := supervisor.run(ctx, policy, observe, clock); err == nil || runs.Load() != 1 {
		t.Fatal("replayed workload accepted")
	}
	roots, cert, clientCert, _, pin, clientPin := githubPreviewTestPKI(t)
	service, err := newGitHubSignerService(githubIndependentSigner{signerKey, supervisorPub}, policy, clientPin, observe, clock)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(service)
	server.TLS = &tls.Config{}
	server.TLS.MinVersion = tls.VersionTLS13
	server.TLS.Certificates = []tls.Certificate{cert}
	server.TLS.ClientAuth = tls.RequireAndVerifyClientCert
	server.TLS.ClientCAs = roots
	server.StartTLS()
	defer server.Close()
	client, err := newGitHubSignerClient(server.URL+"/v1/sign", roots, clientCert, pin, signerPub)
	if err != nil {
		t.Fatal(err)
	}
	ev, rel, err := observe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before := observations.Load()
	changed.Store(true)
	if _, err := client.sign(ctx, policy, ev, rel, receipt, signature, clock); err == nil || observations.Load() != before+1 {
		t.Fatal("signer accepted a superseded attempt")
	}
	changed.Store(false)
	proof, err := client.sign(ctx, policy, ev, rel, receipt, signature, clock)
	if err != nil {
		t.Fatal("independent signing failed", err)
	}
	if _, err := verifyGitHubTaskExecution(policy.task, p, ev, policy.sourceDigest, nil, rel, proof, signerPub, now); err != nil {
		t.Fatal("controller rejected independently signed evidence", err)
	}
	wrong := policy.task
	wrong.revision++
	if _, err := verifyGitHubTaskExecution(wrong, p, ev, policy.sourceDigest, nil, rel, proof, signerPub, now); err == nil {
		t.Fatal("proof accepted for another revision")
	}
	if _, err := verifyGitHubTaskExecution(policy.task, p, ev, policy.sourceDigest, nil, rel, proof, supervisorPub, now); err == nil {
		t.Fatal("supervisor key accepted as independent signer")
	}
	stale := now.Add(githubEvidenceLifetime)
	if _, err := verifyGitHubTaskExecution(policy.task, p, ev, policy.sourceDigest, nil, rel, proof, signerPub, stale); err == nil {
		t.Fatal("expired observation accepted")
	}
	tampered := append([]byte(nil), signature...)
	tampered[0] ^= 1
	before = observations.Load()
	if _, err := client.sign(ctx, policy, ev, rel, receipt, tampered, clock); err == nil || observations.Load() != before {
		t.Fatal("tampered receipt reached trusted observer")
	}
}
