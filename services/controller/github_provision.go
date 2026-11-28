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
	var ok bool
	if err := s.db.QueryRowContext(ctx, `SELECT browseai.provision_github_binding($1,$2,$3,$4,$5,$6)`,
		b.tenantID, b.installationID, b.repositoryID, b.fullName, approval.appID, approval.accountID).Scan(&ok); err != nil || !ok {
		return errGitHubAuthorization
	}
	return nil
}
