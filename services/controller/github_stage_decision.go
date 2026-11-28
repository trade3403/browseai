package main

import (
	"context"
	"database/sql"
)

// Only an independently authenticated and explicitly reviewed human decision
// may reach this owner-side adapter. It does not dispatch or release leases.
func decideGitHubStage(ctx context.Context, tx *sql.Tx, tenant, task, acceptance, actor, decisionID string, revision uint64, action taskAction) error {
	if tx == nil || ctx.Err() != nil || !resourceUUID.MatchString(tenant) || !resourceUUID.MatchString(task) ||
		!resourceUUID.MatchString(acceptance) || !resourceUUID.MatchString(actor) || !resourceUUID.MatchString(decisionID) ||
		acceptance == decisionID || revision == 0 || revision >= uint64(1<<63-1) ||
		(action != actionNext && action != actionComplete) {
		return errGitHubAuthorization
	}
	p := principal{tenantID: tenant}
	r, err := lockedTask(ctx, tx, p, task, revision)
	if err != nil || r.State != taskStagePassed {
		return errGitHubAuthorization
	}
	if err := requireStagePlan(ctx, tx, p, r); err != nil {
		return errGitHubAuthorization
	}
	if _, err := advanceTask(r.snapshot(), revision, action, taskEvidence{FinalAccepted: action == actionComplete}); err != nil {
		return errGitHubAuthorization
	}
	var ok bool
	err = tx.QueryRowContext(ctx, "SELECT browseai.decide_github_stage($1,$2,$3,$4,$5,$6,$7)",
		tenant, task, int64(revision), acceptance, actor, decisionID, string(action)).Scan(&ok)
	if err != nil || !ok || ctx.Err() != nil {
		return errGitHubAuthorization
	}
	return nil
}
