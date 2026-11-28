package main

import (
	"context"
	"database/sql"
	"time"
)

const githubRepairHumanSQL = "SELECT browseai.review_authorized_github_repair($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)"

// No raw session token is persisted or sent to SQL. Confirmation records
// a human's report only; it does not perform or independently prove a send.
func reviewAuthorizedGitHubRepair(ctx context.Context, db *sql.DB, token, tenant, task, grant, failure, approval, confirmation string, revision, fence int64, instruction []byte, action string, manualSent bool) error {
	digest, err := digestSession(token)
	if err != nil || db == nil || ctx.Err() != nil || revision < 2 || revision == 1<<63-1 || fence <= 0 ||
		!resourceUUID.MatchString(tenant) || !resourceUUID.MatchString(task) || !resourceUUID.MatchString(grant) ||
		!resourceUUID.MatchString(failure) || !resourceUUID.MatchString(approval) || !validPlanText(string(instruction), 16384) {
		return errGitHubAuthorization
	}
	var receipt any
	switch action {
	case "approve":
		if manualSent || confirmation != "" {
			return errGitHubAuthorization
		}
	case "confirm_sent":
		if !manualSent || !resourceUUID.MatchString(confirmation) || confirmation == approval {
			return errGitHubAuthorization
		}
		receipt = confirmation
	default:
		return errGitHubAuthorization
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var ok bool
	err = db.QueryRowContext(ctx, githubRepairHumanSQL, digest[:], tenant, task, revision, grant, failure, approval, receipt, fence, instruction, action).Scan(&ok)
	if err != nil || !ok || ctx.Err() != nil {
		return errGitHubAuthorization
	}
	return nil
}
