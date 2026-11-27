package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"time"
)

const githubTaskExecutionDomain = "browseai-ci-task-execution-v2"

type githubEvidenceTask struct {
	projectID string
	taskID    string
	revision  int64
}

func (t githubEvidenceTask) valid() bool {
	return resourceUUID.MatchString(t.projectID) && resourceUUID.MatchString(t.taskID) && t.revision >= 0
}

// The outer domain distinguishes a task-bound statement from execution-only
// v1. Identifiers are signed, not merely attached as unsigned metadata.
type githubTaskExecutionStatement struct {
	Domain    string                   `json:"domain"`
	Project   string                   `json:"project"`
	Task      string                   `json:"task"`
	Revision  int64                    `json:"revision"`
	Verifier  string                   `json:"verifier"`
	Key       string                   `json:"key"`
	Execution githubExecutionStatement `json:"execution"`
}

func githubTaskExecutionMessage(t githubEvidenceTask, p githubEvidencePolicy, e githubCIEvidence, digest string, pr *githubPRPolicy, rel githubPRRelationship, proof githubRegisteredProof) githubTaskExecutionStatement {
	return githubTaskExecutionStatement{
		Domain: githubTaskExecutionDomain, Project: t.projectID, Task: t.taskID,
		Revision: t.revision, Verifier: proof.verifierID, Key: proof.keyID,
		Execution: githubExecutionMessage(p, e, digest, pr, rel, proof.proof.issuedUnix),
	}
}

// This is still a candidate, not an authorization to advance a task.
// The ledger writer must recheck the current task, binding and key under locks.
type githubVerifiedTaskEvidence struct {
	statement []byte
	signature []byte
	digest    [32]byte
	observed  time.Time
	issued    time.Time
	expires   time.Time
}

func verifyGitHubTaskExecution(t githubEvidenceTask, p githubEvidencePolicy, e githubCIEvidence, digest string, pr *githubPRPolicy, rel githubPRRelationship, proof githubRegisteredProof, key ed25519.PublicKey, now time.Time) (githubVerifiedTaskEvidence, error) {
	if !t.valid() || !resourceUUID.MatchString(proof.verifierID) || !resourceUUID.MatchString(proof.keyID) {
		return githubVerifiedTaskEvidence{}, errGitHubAuthorization
	}
	if err := validateGitHubExecution(p, e, digest, pr, rel, proof.proof, key, now); err != nil {
		return githubVerifiedTaskEvidence{}, err
	}
	raw, err := json.Marshal(githubTaskExecutionMessage(t, p, e, digest, pr, rel, proof))
	if err != nil || len(raw) > 131072 || !ed25519.Verify(key, raw, proof.proof.signature) {
		return githubVerifiedTaskEvidence{}, errGitHubAuthorization
	}
	signature := append([]byte(nil), proof.proof.signature...)
	issued := time.Unix(proof.proof.issuedUnix, 0).UTC()
	expires := e.observedAt.Add(githubEvidenceLifetime)
	if end := issued.Add(githubExecutionMaxAge); end.Before(expires) {
		expires = end
	}
	h := sha256.New()
	h.Write(raw)
	h.Write(signature)
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return githubVerifiedTaskEvidence{raw, signature, sum, e.observedAt, issued, expires}, nil
}

func (s githubVerifierStore) verifyTaskObserved(ctx context.Context, t githubEvidenceTask, p githubEvidencePolicy, e githubCIEvidence, digest string, pr *githubPRPolicy, rel githubPRRelationship, proof githubRegisteredProof, clock func() time.Time) (githubVerifiedTaskEvidence, error) {
	if clock == nil || !t.valid() || !e.freshFor(p, clock()) {
		return githubVerifiedTaskEvidence{}, errGitHubAuthorization
	}
	key, err := s.key(ctx, p.binding, proof)
	if err != nil {
		return githubVerifiedTaskEvidence{}, err
	}
	return verifyGitHubTaskExecution(t, p, e, digest, pr, rel, proof, key, clock())
}

func (s githubVerifierStore) verifyTaskPush(ctx context.Context, a *githubApp, t githubEvidenceTask, p githubEvidencePolicy, digest string, proof githubRegisteredProof, clock func() time.Time) (githubVerifiedTaskEvidence, error) {
	if s.db == nil || a == nil || clock == nil || !t.valid() {
		return githubVerifiedTaskEvidence{}, errGitHubAuthorization
	}
	e, err := a.observePushEvidence(ctx, p, clock)
	if err != nil {
		return githubVerifiedTaskEvidence{}, err
	}
	if err := a.verifyPushWorkflowSource(ctx, p, digest, clock()); err != nil {
		return githubVerifiedTaskEvidence{}, err
	}
	return s.verifyTaskObserved(ctx, t, p, e, digest, nil, githubPRRelationship{}, proof, clock)
}

func (s githubVerifierStore) verifyTaskPR(ctx context.Context, a *githubApp, t githubEvidenceTask, p githubPRPolicy, digest string, proof githubRegisteredProof, clock func() time.Time) (githubVerifiedTaskEvidence, error) {
	if s.db == nil || a == nil || clock == nil || !t.valid() {
		return githubVerifiedTaskEvidence{}, errGitHubAuthorization
	}
	c, err := a.observePRCandidate(ctx, p, clock)
	if err != nil {
		return githubVerifiedTaskEvidence{}, err
	}
	if err := a.verifyPRWorkflowSource(ctx, p, c.relationship, digest, clock()); err != nil {
		return githubVerifiedTaskEvidence{}, err
	}
	e := githubCIEvidence{c.policy.push, c.runID, c.attempt, c.jobIDs, c.observedAt}
	return s.verifyTaskObserved(ctx, t, p.push, e, digest, &p, c.relationship, proof, clock)
}
