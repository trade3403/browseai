package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"time"
)

// The writer login is a trusted service credential, never exposed to API
// users, repositories, or the independent execution verifier.
type githubEvidenceWriter struct {
	db       *sql.DB
	verifier githubVerifierStore
}

func (w githubEvidenceWriter) writePush(ctx context.Context, a *githubApp, t githubEvidenceTask, p githubEvidencePolicy, digest string, proof githubRegisteredProof, clock func() time.Time) (string, error) {
	v, err := w.verifier.verifyTaskPush(ctx, a, t, p, digest, proof, clock)
	if err != nil {
		return "", err
	}
	return w.insert(ctx, v, clock())
}

func (w githubEvidenceWriter) writePR(ctx context.Context, a *githubApp, t githubEvidenceTask, p githubPRPolicy, digest string, proof githubRegisteredProof, clock func() time.Time) (string, error) {
	v, err := w.verifier.verifyTaskPR(ctx, a, t, p, digest, proof, clock)
	if err != nil {
		return "", err
	}
	return w.insert(ctx, v, clock())
}

// Only the verified composite paths above may call insert. SQL derives signed
// columns, rechecks live authorization, and enforces uniqueness atomically.
func (w githubEvidenceWriter) insert(ctx context.Context, v githubVerifiedTaskEvidence, now time.Time) (string, error) {
	if w.db == nil || len(v.statement) == 0 || len(v.statement) > 131072 ||
		len(v.signature) != ed25519.SignatureSize || now.Before(v.observed) ||
		now.Before(v.issued) || !now.Before(v.expires) ||
		v.digest != sha256.Sum256(append(append([]byte(nil), v.statement...), v.signature...)) {
		return "", errGitHubAuthorization
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var id sql.NullString
	err := w.db.QueryRowContext(ctx, `SELECT CASE WHEN current_user='browseai_evidence_writer'
AND session_user=current_user AND NOT r.rolsuper AND NOT r.rolbypassrls
AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
AND NOT has_function_privilege(current_user,'browseai.register_github_verifier(uuid,bigint,bigint,uuid,uuid,bytea,timestamptz,timestamptz,uuid)','EXECUTE')
AND NOT has_function_privilege(current_user,'browseai.revoke_github_verifier(uuid,bigint,bigint,uuid,uuid)','EXECUTE')
AND NOT has_function_privilege(current_user,'browseai.resolve_github_verifier(uuid,bigint,bigint,uuid,uuid,timestamptz)','EXECUTE')
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='browseai' AND c.relkind IN ('r','p','v','m','f')
AND has_table_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER'))
THEN browseai.insert_github_ci_evidence($1,$2,$3)::text ELSE NULL END
FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`, v.statement, v.signature, v.observed).Scan(&id)
	if err != nil || !id.Valid || !resourceUUID.MatchString(id.String) || ctx.Err() != nil {
		return "", errGitHubAuthorization
	}
	return id.String, nil
}
