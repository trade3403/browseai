package main

import (
	"context"
	"database/sql"
	"time"

	"github.com/lib/pq"
)

// Trusted owner-side transaction only. The candidate must come directly
// from observeCIFailure, never from an HTTP body or a persisted monitor flag.
func (c githubCIFailureCandidate) record(ctx context.Context, tx *sql.Tx, clock func() time.Time) (string, error) {
	if tx == nil || clock == nil || ctx.Err() != nil || !c.task.valid() || !c.policy.valid() ||
		!githubWorkflowDigestValid(c.source) || c.runID <= 0 || c.attempt <= 0 ||
		(c.event != "push" && c.event != "pull_request") || len(c.failedIDs) == 0 || len(c.failedIDs) > 100 ||
		c.observedAt.IsZero() {
		return "", errGitHubAuthorization
	}
	now := clock()
	if now.Before(c.observedAt) || !now.Before(c.observedAt.Add(githubEvidenceLifetime)) {
		return "", errGitHubAuthorization
	}
	var id sql.NullString
	err := tx.QueryRowContext(ctx, "SELECT browseai.record_github_ci_failure($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)::text",
		c.policy.binding.tenantID, c.task.taskID, c.task.revision, c.task.projectID,
		c.policy.binding.installationID, c.policy.binding.repositoryID, c.policy.branch, c.policy.sha,
		c.policy.workflowID, c.source, c.event, c.runID, c.attempt, pq.Array(c.failedIDs), c.observedAt).Scan(&id)
	if err != nil || !id.Valid || !resourceUUID.MatchString(id.String) || ctx.Err() != nil {
		return "", errGitHubAuthorization
	}
	return id.String, nil
}

// No instruction is sent. A repair reservation uses the same stage-plan
// and lease checks as the existing internal state machine.
func reserveGitHubCIRepair(ctx context.Context, tx *sql.Tx, tenant, task, failure string, revision uint64, fence int64) (taskState, error) {
	if tx == nil || ctx.Err() != nil || !resourceUUID.MatchString(tenant) || !resourceUUID.MatchString(task) ||
		!resourceUUID.MatchString(failure) || revision == 0 || revision >= uint64(1<<63-1) || fence <= 0 {
		return "", errGitHubAuthorization
	}
	p := principal{tenantID: tenant}
	r, err := lockedTask(ctx, tx, p, task, revision)
	if err != nil || r.State != taskCIFailed {
		return "", errGitHubAuthorization
	}
	if err := requireLiveLease(ctx, tx, p, task, fence); err != nil {
		return "", errGitHubAuthorization
	}
	if err := requireStagePlan(ctx, tx, p, r); err != nil {
		return "", errGitHubAuthorization
	}
	var target sql.NullString
	err = tx.QueryRowContext(ctx, "SELECT browseai.reserve_github_ci_repair($1,$2,$3,$4,$5)", tenant, task, int64(revision), failure, fence).Scan(&target)
	if err != nil || !target.Valid || ctx.Err() != nil || (target.String != string(taskAwaitApproval) && target.String != string(taskNeedHuman)) {
		return "", errGitHubAuthorization
	}
	return taskState(target.String), nil
}
