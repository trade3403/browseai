package main

import (
	"context"
	"database/sql"
	"time"
)

const githubCIAuthorizedSQL = "SELECT browseai.accept_authorized_github_ci($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)"
const githubCIGrantSQL = "SELECT browseai.issue_github_ci_acceptance_grant($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)"

func (p githubCIAcceptancePlan) authorizedArgs() []any {
	a := p.approval
	return []any{a.approvalID, a.tenantID, a.task.taskID, a.task.revision, a.actorID, a.pushID, a.pushDigest[:], a.prID, a.prDigest[:], p.workflow, p.source}
}

// The caller must obtain the prepared plan from independent remote
// verification, not deserialize it from an untrusted request. SQL checks
// the independently issued grant and rechecks all ledger constraints.
func (p githubCIAcceptancePlan) applyAuthorized(ctx context.Context, tx *sql.Tx, clock func() time.Time) error {
	if tx == nil || clock == nil || ctx.Err() != nil || !clock().Before(p.expires) ||
		p.workflow <= 0 || !githubWorkflowDigestValid(p.source) {
		return errGitHubAuthorization
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var ok bool
	err := tx.QueryRowContext(ctx, githubCIAuthorizedSQL, p.authorizedArgs()...).Scan(&ok)
	if err != nil || !ok || ctx.Err() != nil {
		return errGitHubAuthorization
	}
	return nil
}

func githubCIAcceptanceRoleReady(ctx context.Context, db *sql.DB, role string) error {
	if db == nil || (role != "browseai_ci_acceptor" && role != "browseai_ci_approval_operator") {
		return errGitHubAuthorization
	}
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT current_user=$1 AND session_user=current_user
AND NOT r.rolsuper AND NOT r.rolbypassrls AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
AND CASE WHEN $1='browseai_ci_acceptor' THEN
has_function_privilege(current_user,'browseai.accept_authorized_github_ci(uuid,uuid,uuid,bigint,uuid,uuid,bytea,uuid,bytea,bigint,text)','EXECUTE')
AND has_function_privilege(current_user,'browseai.read_github_acceptance_evidence(uuid,uuid,uuid,bigint,uuid)','EXECUTE')
ELSE has_function_privilege(current_user,'browseai.issue_github_ci_acceptance_grant(uuid,uuid,uuid,bigint,uuid,uuid,bytea,uuid,bytea,bigint,text)','EXECUTE')
AND has_function_privilege(current_user,'browseai.revoke_github_ci_acceptance_grant(uuid,uuid)','EXECUTE') END
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
WHERE n.nspname='browseai' AND has_function_privilege(current_user,p.oid,'EXECUTE')
AND NOT (($1='browseai_ci_acceptor' AND p.proname IN ('accept_authorized_github_ci','read_github_acceptance_evidence'))
OR ($1='browseai_ci_approval_operator' AND p.proname IN ('issue_github_ci_acceptance_grant','revoke_github_ci_acceptance_grant'))))
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='browseai' AND c.relkind IN ('r','p','v','m','f')
AND (has_table_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
OR has_any_column_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,REFERENCES')))
FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`, role).Scan(&ok)
	if err != nil || !ok {
		return errGitHubAuthorization
	}
	return nil
}
