package main

import (
	"context"
	"database/sql"
	"errors"
)

type githubBindingStore struct {
	db *sql.DB
}

// Resolve the project using the authenticated principal and independently
// provisioned stable repository mapping. User-entered repository IDs alone
// confer no access. Disabled bindings and name mismatches fail closed.
func (s githubBindingStore) forProject(ctx context.Context, p principal, projectID string) (githubBinding, error) {
	if !resourceUUID.MatchString(projectID) {
		return githubBinding{}, errResourceMissing
	}
	return tenantTx(ctx, s.db, p, func(tx *sql.Tx) (githubBinding, error) {
		var b githubBinding
		err := tx.QueryRowContext(ctx, `SELECT i.tenant_id::text,i.installation_id,r.repository_id,r.full_name
FROM browseai.projects p
JOIN browseai.github_repositories r ON r.tenant_id=p.tenant_id AND r.repository_id=p.repository_id
JOIN browseai.github_installations i ON i.tenant_id=r.tenant_id AND i.installation_id=r.installation_id
WHERE p.tenant_id=$1 AND p.id=$2 AND i.enabled AND r.enabled AND lower(r.full_name)=lower(p.repository)`, p.tenantID, projectID).Scan(&b.tenantID, &b.installationID, &b.repositoryID, &b.fullName)
		if errors.Is(err, sql.ErrNoRows) {
			return githubBinding{}, errResourceMissing
		}
		return b, err
	})
}
