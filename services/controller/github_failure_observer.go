package main

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"
)

const githubAuthorizedFailureSQL = "SELECT browseai.record_authorized_github_failure($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)::text"

func githubFailureObserverReady(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errGitHubAuthorization
	}
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT current_user='browseai_failure_observer' AND session_user=current_user
AND NOT r.rolsuper AND NOT r.rolbypassrls AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
AND has_function_privilege(current_user,'browseai.record_authorized_github_failure(uuid,uuid,uuid,bigint,uuid,bigint,bigint,text,text,bigint,text,text,bigint,bigint,bigint[],timestamptz)','EXECUTE')
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
WHERE n.nspname='browseai' AND p.proname<>'record_authorized_github_failure'
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

// Only the independent observer receives this DB identity. The caller must
// load the policy and grant ID from trusted configuration, never HTTP input.
func observeAndRecordGitHubFailure(ctx context.Context, db *sql.DB, app *githubApp, policy githubIndependentPolicy, grant string, clock func() time.Time) (string, error) {
	if !resourceUUID.MatchString(grant) || clock == nil || ctx.Err() != nil {
		return "", errGitHubAuthorization
	}
	check, cancel := context.WithTimeout(ctx, 3*time.Second)
	err := githubFailureObserverReady(check, db)
	cancel()
	if err != nil {
		return "", err
	}
	c, err := app.observeCIFailure(ctx, policy, clock)
	if err != nil {
		return "", err
	}
	return recordAuthorizedGitHubFailure(ctx, db, c, grant, clock)
}

func recordAuthorizedGitHubFailure(ctx context.Context, db *sql.DB, c githubCIFailureCandidate, grant string, clock func() time.Time) (string, error) {
	if db == nil || clock == nil || !resourceUUID.MatchString(grant) || !c.task.valid() || !c.policy.valid() ||
		!githubWorkflowDigestValid(c.source) || c.observedAt.IsZero() || c.runID <= 0 || c.attempt <= 0 ||
		(c.event != "push" && c.event != "pull_request") || len(c.failedIDs) == 0 || len(c.failedIDs) > 100 {
		return "", errGitHubAuthorization
	}
	now := clock()
	if now.Before(c.observedAt) || !now.Before(c.observedAt.Add(githubEvidenceLifetime)) || ctx.Err() != nil {
		return "", errGitHubAuthorization
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var id sql.NullString
	err := db.QueryRowContext(ctx, githubAuthorizedFailureSQL, grant, c.policy.binding.tenantID, c.task.taskID,
		c.task.revision, c.task.projectID, c.policy.binding.installationID, c.policy.binding.repositoryID,
		c.policy.branch, c.policy.sha, c.policy.workflowID, c.source, c.event, c.runID, c.attempt, pq.Array(c.failedIDs), c.observedAt).Scan(&id)
	if err != nil || !id.Valid || !resourceUUID.MatchString(id.String) {
		return "", errGitHubAuthorization
	}
	return id.String, nil
}
