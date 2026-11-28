package main

import (
	"context"
	"database/sql"
)

// A trusted human report is required. This adapter never sends a message,
// reads a ChatGPT conversation or treats approval alone as a delivery receipt.
func confirmGitHubCIRepair(ctx context.Context, tx *sql.Tx, tenant, task, approval, actor, confirmation string, revision uint64, fence int64, instruction []byte, manualSent bool) error {
	if tx == nil || ctx.Err() != nil || !manualSent || !resourceUUID.MatchString(tenant) ||
		!resourceUUID.MatchString(task) || !resourceUUID.MatchString(approval) ||
		!resourceUUID.MatchString(actor) || !resourceUUID.MatchString(confirmation) || approval == confirmation ||
		revision < 2 || revision >= uint64(1<<63-1) || fence <= 0 || !validPlanText(string(instruction), 16384) {
		return errGitHubAuthorization
	}
	p := principal{tenantID: tenant}
	r, err := lockedTask(ctx, tx, p, task, revision)
	if err != nil || r.State != taskAwaitApproval || r.Repairs <= 0 {
		return errGitHubAuthorization
	}
	if err := requireLiveLease(ctx, tx, p, task, fence); err != nil {
		return errGitHubAuthorization
	}
	if err := requireStagePlan(ctx, tx, p, r); err != nil {
		return errGitHubAuthorization
	}
	if _, err := advanceTask(r.snapshot(), revision, actionConfirmSent, taskEvidence{ManualSent: true, ReadyToRun: true}); err != nil {
		return errGitHubAuthorization
	}
	var ok bool
	err = tx.QueryRowContext(ctx, "SELECT browseai.confirm_github_ci_repair($1,$2,$3,$4,$5,$6,$7,$8)",
		tenant, task, int64(revision), approval, actor, confirmation, fence, instruction).Scan(&ok)
	if err != nil || !ok || ctx.Err() != nil {
		return errGitHubAuthorization
	}
	return nil
}
