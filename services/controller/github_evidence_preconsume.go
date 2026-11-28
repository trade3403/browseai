package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"time"
)

// Recheck the original evidence envelope. A fresh observation cannot renew
// the lifetime of a persisted row, even when the signed bytes are unchanged.
func githubEvidenceEnvelopeValid(v githubVerifiedTaskEvidence, now time.Time) bool {
	return len(v.statement) > 0 && len(v.statement) <= 131072 &&
		len(v.signature) == ed25519.SignatureSize && !v.observed.IsZero() &&
		!v.issued.IsZero() && !now.Before(v.observed) && !now.Before(v.issued) &&
		v.expires.After(v.observed) && v.expires.After(v.issued) && now.Before(v.expires) &&
		!v.expires.After(v.observed.Add(githubEvidenceLifetime)) &&
		!v.expires.After(v.issued.Add(githubExecutionMaxAge)) &&
		v.digest == sha256.Sum256(append(append([]byte(nil), v.statement...), v.signature...))
}

// The policy comes from a trusted operator/configuration, never from the
// persisted statement. This is only a read-only preflight. A future consumer
// must still lock and recheck the same evidence digest and task in SQL,
// consume every required row and transition atomically.
func (s githubVerifierStore) recheckTaskEvidence(ctx context.Context, app *githubApp, policy githubIndependentPolicy, stored githubVerifiedTaskEvidence, clock func() time.Time) error {
	if s.db == nil || clock == nil || ctx.Err() != nil || !policy.task.valid() ||
		!resourceUUID.MatchString(policy.verifierID) || !resourceUUID.MatchString(policy.keyID) ||
		!githubEvidenceEnvelopeValid(stored, clock()) {
		return errGitHubAuthorization
	}
	// Take ownership of the bounded input before remote observation.
	stored.statement = append([]byte(nil), stored.statement...)
	stored.signature = append([]byte(nil), stored.signature...)
	var statement githubTaskExecutionStatement
	if json.Unmarshal(stored.statement, &statement) != nil {
		return errGitHubAuthorization
	}
	raw, err := json.Marshal(statement)
	if err != nil || !bytes.Equal(raw, stored.statement) ||
		statement.Domain != githubTaskExecutionDomain || statement.Project != policy.task.projectID ||
		statement.Task != policy.task.taskID || statement.Revision != policy.task.revision ||
		statement.Verifier != policy.verifierID || statement.Key != policy.keyID ||
		!stored.issued.Equal(time.Unix(statement.Execution.IssuedUnix, 0)) {
		return errGitHubAuthorization
	}
	observe, err := newGitHubTrustedSignerObserver(app, policy, clock)
	if err != nil {
		return errGitHubAuthorization
	}
	e, rel, err := observe(ctx)
	if err != nil {
		return errGitHubAuthorization
	}
	proof := githubRegisteredProof{policy.verifierID, policy.keyID, githubExecutionProof{statement.Execution.IssuedUnix, stored.signature}}
	fresh, err := s.verifyTaskObserved(ctx, policy.task, policy.execution, e, policy.sourceDigest, policy.pr, rel, proof, clock)
	if err != nil || ctx.Err() != nil || !githubEvidenceEnvelopeValid(stored, clock()) ||
		fresh.digest != stored.digest || !bytes.Equal(fresh.statement, stored.statement) ||
		!bytes.Equal(fresh.signature, stored.signature) {
		return errGitHubAuthorization
	}
	return nil
}
