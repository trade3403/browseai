package main

import (
	"context"
	"database/sql"
	"time"
)

const githubRepairReserverSQL = "SELECT browseai.reserve_authorized_github_repair($1,$2,$3,$4,$5,$6)"

func githubRepairReserverReady(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errGitHubAuthorization
	}
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT current_user='browseai_repair_reserver' AND session_user=current_user
AND NOT r.rolsuper AND NOT r.rolbypassrls AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
AND has_function_privilege(current_user,'browseai.reserve_authorized_github_repair(uuid,uuid,uuid,bigint,uuid,bigint)','EXECUTE')
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
WHERE n.nspname='browseai' AND p.proname<>'reserve_authorized_github_repair'
AND has_function_privilege(current_user,p.oid,'EXECUTE'))
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='browseai' AND c.relkind IN ('r','p','v','m','f')
AND (has_table_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
OR has_any_column_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,REFERENCES')))
FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`).Scan(&ok)
	if err != nil || !ok {
		return errGitHubAuthorization
	}
	return nil
}

// The reservation consumes the already-recorded failure under its task
// lock. It never approves instructions or sends anything to a browser.
func reserveAuthorizedGitHubRepair(ctx context.Context, db *sql.DB, grant, tenant, task, failure string, revision int64, fence int64) (taskState, error) {
	if ctx.Err() != nil || db == nil || !resourceUUID.MatchString(grant) || !resourceUUID.MatchString(tenant) ||
		!resourceUUID.MatchString(task) || !resourceUUID.MatchString(failure) || revision <= 0 || revision == 1<<63-1 || fence <= 0 {
		return "", errGitHubAuthorization
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := githubRepairReserverReady(ctx, db); err != nil {
		return "", err
	}
	var target sql.NullString
	err := db.QueryRowContext(ctx, githubRepairReserverSQL, grant, tenant, task, revision, failure, fence).Scan(&target)
	if err != nil || !target.Valid || ctx.Err() != nil || (target.String != string(taskAwaitApproval) && target.String != string(taskNeedHuman)) {
		return "", errGitHubAuthorization
	}
	return taskState(target.String), nil
}
