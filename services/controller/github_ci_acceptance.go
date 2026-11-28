package main

import (
	"context"
	"database/sql"
	"time"
)

// This approval must originate from a trusted human acceptance channel.
// It is not a public request DTO or evidence of an authenticated session.
type githubCIAcceptanceApproval struct {
	actorID    string
	approvalID string
	tenantID   string
	task       githubEvidenceTask
	pushID     string
	prID       string
	pushDigest [32]byte
	prDigest   [32]byte
}

type githubCIAcceptancePlan struct {
	approval githubCIAcceptanceApproval
	workflow int64
	source   string
	expires  time.Time
}

// Prepare exactly two proofs under one independently provisioned policy.
// Both events are freshly observed and reverified against registered keys;
// an old envelope's lifetime is never extended. This performs no writes.
func (s githubVerifierStore) prepareCIAcceptance(ctx context.Context, pushApp, prApp *githubApp, policy githubIndependentPolicy, approval githubCIAcceptanceApproval, push, pr githubVerifiedTaskEvidence, clock func() time.Time) (githubCIAcceptancePlan, error) {
	fail := func() (githubCIAcceptancePlan, error) { return githubCIAcceptancePlan{}, errGitHubAuthorization }
	if s.db == nil || clock == nil || ctx.Err() != nil || policy.pr == nil ||
		!policy.task.valid() || approval.task != policy.task ||
		approval.tenantID != policy.execution.binding.tenantID ||
		!resourceUUID.MatchString(approval.actorID) || !resourceUUID.MatchString(approval.approvalID) ||
		!resourceUUID.MatchString(approval.pushID) || !resourceUUID.MatchString(approval.prID) ||
		approval.pushID == approval.prID || approval.pushDigest != push.digest ||
		approval.prDigest != pr.digest || push.digest == pr.digest {
		return fail()
	}
	ctx, cancel := context.WithTimeout(ctx, githubEvidenceLifetime)
	defer cancel()
	pushPolicy := policy
	pushPolicy.pr = nil
	if err := s.recheckTaskEvidence(ctx, pushApp, pushPolicy, push, clock); err != nil {
		return fail()
	}
	if err := s.recheckTaskEvidence(ctx, prApp, policy, pr, clock); err != nil {
		return fail()
	}
	now := clock()
	if ctx.Err() != nil || !githubEvidenceEnvelopeValid(push, now) || !githubEvidenceEnvelopeValid(pr, now) {
		return fail()
	}
	expires := push.expires
	if pr.expires.Before(expires) {
		expires = pr.expires
	}
	return githubCIAcceptancePlan{approval, policy.execution.workflowID, policy.sourceDigest, expires}, nil
}

// The sealed SQL primitive checks the same row IDs and digests under locks.
// The caller owns the transaction and must roll it back on any error.
// No production role is granted this call, and no runtime route invokes it.
func (p githubCIAcceptancePlan) apply(ctx context.Context, tx *sql.Tx, clock func() time.Time) error {
	if tx == nil || clock == nil || ctx.Err() != nil || !clock().Before(p.expires) ||
		p.workflow <= 0 || !githubWorkflowDigestValid(p.source) {
		return errGitHubAuthorization
	}
	a := p.approval
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var ok bool
	err := tx.QueryRowContext(ctx, "SELECT browseai.accept_github_ci($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)",
		a.tenantID, a.task.taskID, a.task.revision, a.actorID, a.approvalID,
		a.pushID, a.pushDigest[:], a.prID, a.prDigest[:], p.workflow, p.source).Scan(&ok)
	if err != nil || !ok || ctx.Err() != nil {
		return errGitHubAuthorization
	}
	return nil
}
