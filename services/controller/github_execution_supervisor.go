package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

const githubSupervisorTimeout = 30 * time.Minute

// Only trusted supervisor infrastructure can implement this adapter. It must
// enforce immutable checkout, pinned harness/image/dependencies and isolation
// outside repository control. Child process stdout is not a trusted report.
type githubIsolatedExecutor interface {
	execute(context.Context, githubIsolatedRequest) (githubIsolatedReport, error)
}

type githubIsolatedRequest struct {
	statement     githubTaskExecutionStatement
	harnessDigest string
	imageDigest   string
}

type githubIsolatedJob struct {
	id     int64
	name   string
	passed bool
}

type githubIsolatedReport struct {
	checkoutSHA   string
	harnessDigest string
	imageDigest   string
	jobs          []githubIsolatedJob
}

type githubSupervisor struct {
	privateKey ed25519.PrivateKey
	executor   githubIsolatedExecutor
}

// Refresh is supplied by the trusted remote observer. Recheck the exact
// workflow and attempt after the potentially long execution, not just before.
type githubSupervisorObserve func(context.Context) (githubCIEvidence, githubPRRelationship, error)

func githubSupervisorStatement(policy githubIndependentPolicy, e githubCIEvidence, rel githubPRRelationship, key ed25519.PublicKey, now time.Time) (githubTaskExecutionStatement, error) {
	if !policy.task.valid() || !resourceUUID.MatchString(policy.verifierID) || !resourceUUID.MatchString(policy.keyID) ||
		!githubWorkflowDigestValid(policy.harnessDigest) || !githubWorkflowDigestValid(policy.imageDigest) {
		return githubTaskExecutionStatement{}, errGitHubAuthorization
	}
	proof := githubRegisteredProof{
		verifierID: policy.verifierID, keyID: policy.keyID,
		proof: githubExecutionProof{issuedUnix: now.Unix(), signature: make([]byte, ed25519.SignatureSize)},
	}
	if err := validateGitHubExecution(policy.execution, e, policy.sourceDigest, policy.pr, rel, proof.proof, key, now); err != nil {
		return githubTaskExecutionStatement{}, err
	}
	return githubTaskExecutionMessage(policy.task, policy.execution, e, policy.sourceDigest, policy.pr, rel, proof), nil
}

func githubSupervisorReportValid(req githubIsolatedRequest, report githubIsolatedReport) bool {
	if report.checkoutSHA != req.statement.Execution.ExecutedSHA ||
		report.harnessDigest != req.harnessDigest || report.imageDigest != req.imageDigest ||
		len(report.jobs) != len(req.statement.Execution.Jobs) {
		return false
	}
	ids := make(map[int64]bool, len(report.jobs))
	names := make(map[string]bool, len(report.jobs))
	for _, j := range report.jobs {
		if !j.passed || j.id <= 0 || j.name == "" || ids[j.id] || names[j.name] {
			return false
		}
		ids[j.id], names[j.name] = true, true
	}
	for _, id := range req.statement.Execution.Jobs {
		if !ids[id] {
			return false
		}
	}
	for _, name := range req.statement.Execution.Required {
		if !names[name] {
			return false
		}
	}
	return true
}

func (s githubSupervisor) run(ctx context.Context, policy githubIndependentPolicy, observe githubSupervisorObserve, clock func() time.Time) (githubExecutorReceipt, []byte, error) {
	fail := func() (githubExecutorReceipt, []byte, error) {
		return githubExecutorReceipt{}, nil, errGitHubAuthorization
	}
	if s.executor == nil || observe == nil || clock == nil || len(s.privateKey) != ed25519.PrivateKeySize ||
		!bytes.Equal(s.privateKey, ed25519.NewKeyFromSeed(s.privateKey.Seed())) {
		return fail()
	}
	ctx, cancel := context.WithTimeout(ctx, githubSupervisorTimeout)
	defer cancel()
	key := s.privateKey.Public().(ed25519.PublicKey)
	e, rel, err := observe(ctx)
	if err != nil || ctx.Err() != nil {
		return fail()
	}
	start := clock()
	before, err := githubSupervisorStatement(policy, e, rel, key, start)
	if err != nil {
		return fail()
	}
	// Freeze canonical identity before handing a copy to the executor.
	// No keys or database credentials are part of the request.
	before.Execution.IssuedUnix = 0
	frozen, err := json.Marshal(before)
	if err != nil {
		return fail()
	}
	var request githubIsolatedRequest
	if err := json.Unmarshal(frozen, &request.statement); err != nil {
		return fail()
	}
	request.harnessDigest, request.imageDigest = policy.harnessDigest, policy.imageDigest
	report, err := s.executor.execute(ctx, request)
	finished := clock()
	if err != nil || ctx.Err() != nil || finished.Before(start) || finished.Sub(start) >= githubSupervisorTimeout ||
		!githubSupervisorReportValid(githubIsolatedRequest{before, policy.harnessDigest, policy.imageDigest}, report) {
		return fail()
	}
	current, currentRel, err := observe(ctx)
	now := clock()
	if err != nil || ctx.Err() != nil || now.Before(finished) || now.Sub(finished) >= githubExecutorReceiptMaxAge {
		return fail()
	}
	after, err := githubSupervisorStatement(policy, current, currentRel, key, now)
	if err != nil {
		return fail()
	}
	after.Execution.IssuedUnix = 0
	check, err := json.Marshal(after)
	if err != nil || !bytes.Equal(frozen, check) {
		return fail()
	}
	after.Execution.IssuedUnix = finished.Unix()
	raw, err := json.Marshal(after)
	if err != nil || len(raw) > 131072 {
		return fail()
	}
	sum := sha256.Sum256(raw)
	receipt := githubExecutorReceipt{
		Domain: githubExecutorReceiptDomain, StatementDigest: hex.EncodeToString(sum[:]),
		CheckoutSHA: report.checkoutSHA, HarnessDigest: report.harnessDigest, ImageDigest: report.imageDigest,
		StartedUnix: start.Unix(), FinishedUnix: finished.Unix(), AllPassed: true,
	}
	receiptBytes, err := json.Marshal(receipt)
	if err != nil || ctx.Err() != nil {
		return fail()
	}
	return receipt, ed25519.Sign(s.privateKey, receiptBytes), nil
}
