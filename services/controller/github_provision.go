package main

import (
	"context"
	"database/sql"
	"time"
)

// This service is for a separately privileged, trusted operator process.
// Never attach it to a tenant-facing HTTP handler or use its database
// connection in the runtime application. Independent ownership approval
// is a prerequisite, not a consequence of successful GitHub API access.
type githubProvisioner struct {
	db  *sql.DB
	app *githubApp
}

func (s githubProvisioner) provision(ctx context.Context, approval githubInstallationApproval, now time.Time) error {
	if s.db == nil || s.app == nil {
		return errGitHubAuthorization
	}
	if err := s.app.verifyInstallation(ctx, approval, now); err != nil {
		return err
	}
	b := approval.binding
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errGitHubAuthorization
	}
	defer tx.Rollback()
	// No upsert may transfer ownership or silently re-enable a revoked
	// installation. Concurrent inserts are serialized by the primary key.
	if _, err := tx.ExecContext(ctx, `INSERT INTO browseai.github_installations(installation_id,tenant_id)
VALUES($1,$2) ON CONFLICT(installation_id) DO NOTHING`, b.installationID, b.tenantID); err != nil {
		return errGitHubAuthorization
	}
	var enabled bool
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM browseai.github_installations
WHERE installation_id=$1 AND tenant_id=$2 FOR UPDATE`, b.installationID, b.tenantID).Scan(&enabled); err != nil || !enabled {
		return errGitHubAuthorization
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO browseai.github_repositories(tenant_id,installation_id,repository_id,full_name)
VALUES($1,$2,$3,$4) ON CONFLICT(tenant_id,repository_id) DO NOTHING`, b.tenantID, b.installationID, b.repositoryID, b.fullName); err != nil {
		return errGitHubAuthorization
	}
	if err := tx.QueryRowContext(ctx, `SELECT enabled FROM browseai.github_repositories
WHERE tenant_id=$1 AND installation_id=$2 AND repository_id=$3 AND lower(full_name)=lower($4) FOR UPDATE`, b.tenantID, b.installationID, b.repositoryID, b.fullName).Scan(&enabled); err != nil || !enabled {
		return errGitHubAuthorization
	}
	if err := tx.Commit(); err != nil {
		return errGitHubAuthorization
	}
	return nil
}
