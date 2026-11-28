package main

import (
	"context"
	"database/sql"
	"time"
)

const githubStageHumanSQL = "SELECT browseai.review_authorized_github_stage($1,$2,$3,$4,$5,$6,$7)"

// The dedicated database role resolves the actor from a live session.
// A decision never sends an instruction or releases a lease.
func reviewAuthorizedGitHubStage(ctx context.Context, db *sql.DB, token, tenant, task, acceptance, decision string, revision int64, action taskAction, confirmed bool) error {
	digest, err := digestSession(token)
	if err != nil || db == nil || ctx.Err() != nil || !confirmed || revision < 2 || revision == 1<<63-1 ||
		!resourceUUID.MatchString(tenant) || !resourceUUID.MatchString(task) ||
		!resourceUUID.MatchString(acceptance) || !resourceUUID.MatchString(decision) || acceptance == decision ||
		(action != actionNext && action != actionComplete) {
		return errGitHubAuthorization
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var ok bool
	err = db.QueryRowContext(ctx, githubStageHumanSQL, digest[:], tenant, task, revision, acceptance, decision, string(action)).Scan(&ok)
	if err != nil || !ok || ctx.Err() != nil {
		return errGitHubAuthorization
	}
	return nil
}
