package main

import (
	"context"
	"crypto/ed25519"
	"database/sql"
	"time"
)

// Registered proof carries only identifiers and a signature, never a caller
// supplied public key. The key is immutable and globally unique in 0014.
type githubRegisteredProof struct {
	verifierID string
	keyID      string
	proof      githubExecutionProof
}

type githubVerifierStore struct {
	db *sql.DB
}

func (s githubVerifierStore) key(ctx context.Context, b githubBinding, proof githubRegisteredProof) (ed25519.PublicKey, error) {
	if s.db == nil || !resourceUUID.MatchString(b.tenantID) || b.installationID <= 0 || b.repositoryID <= 0 ||
		!resourceUUID.MatchString(proof.verifierID) || !resourceUUID.MatchString(proof.keyID) ||
		proof.proof.issuedUnix <= 0 || len(proof.proof.signature) != ed25519.SignatureSize {
		return nil, errGitHubAuthorization
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// Evaluate the role boundary on the same connection and statement as
	// the lookup. Reject owner, SET ROLE, inherited and table-access logins.
	var key []byte
	err := s.db.QueryRowContext(ctx, `SELECT CASE WHEN current_user='browseai_verifier_reader'
AND session_user=current_user AND NOT r.rolsuper AND NOT r.rolbypassrls
AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
AND NOT has_function_privilege(current_user,'browseai.register_github_verifier(uuid,bigint,bigint,uuid,uuid,bytea,timestamptz,timestamptz,uuid)','EXECUTE')
AND NOT has_function_privilege(current_user,'browseai.revoke_github_verifier(uuid,bigint,bigint,uuid,uuid)','EXECUTE')
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='browseai' AND c.relkind IN ('r','p','v','m','f')
AND has_table_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER'))
THEN browseai.resolve_github_verifier($1,$2,$3,$4,$5,$6) ELSE NULL END
FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`,
		b.tenantID, b.installationID, b.repositoryID, proof.verifierID, proof.keyID, time.Unix(proof.proof.issuedUnix, 0).UTC()).Scan(&key)
	if err != nil || !githubStrictEd25519Key(key) || ctx.Err() != nil {
		return nil, errGitHubAuthorization
	}
	return ed25519.PublicKey(key), nil
}

// A successful observation is still a candidate until a later transaction
// rechecks the key, authorization, task revision and consumes the evidence.
func (s githubVerifierStore) verifyObserved(ctx context.Context, p githubEvidencePolicy, e githubCIEvidence, digest string, pr *githubPRPolicy, rel githubPRRelationship, proof githubRegisteredProof, clock func() time.Time) error {
	if clock == nil || !e.freshFor(p, clock()) {
		return errGitHubAuthorization
	}
	key, err := s.key(ctx, p.binding, proof)
	if err != nil {
		return err
	}
	return verifyGitHubExecution(p, e, digest, pr, rel, proof.proof, key, clock())
}

func (s githubVerifierStore) verifyPushExecution(ctx context.Context, a *githubApp, p githubEvidencePolicy, digest string, proof githubRegisteredProof, clock func() time.Time) error {
	if s.db == nil || a == nil || clock == nil {
		return errGitHubAuthorization
	}
	e, err := a.observePushEvidence(ctx, p, clock)
	if err != nil {
		return err
	}
	if err := a.verifyPushWorkflowSource(ctx, p, digest, clock()); err != nil {
		return err
	}
	return s.verifyObserved(ctx, p, e, digest, nil, githubPRRelationship{}, proof, clock)
}

func (s githubVerifierStore) verifyPRExecution(ctx context.Context, a *githubApp, p githubPRPolicy, digest string, proof githubRegisteredProof, clock func() time.Time) error {
	if s.db == nil || a == nil || clock == nil {
		return errGitHubAuthorization
	}
	c, err := a.observePRCandidate(ctx, p, clock)
	if err != nil {
		return err
	}
	if err := a.verifyPRWorkflowSource(ctx, p, c.relationship, digest, clock()); err != nil {
		return err
	}
	e := githubCIEvidence{c.policy.push, c.runID, c.attempt, c.jobIDs, c.observedAt}
	return s.verifyObserved(ctx, p.push, e, digest, &p, c.relationship, proof, clock)
}
