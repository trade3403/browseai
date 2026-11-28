package main

import (
	"context"
	"database/sql"
)

type githubDeliveryStore struct {
	db *sql.DB
}

// Call only after raw-body authentication. The database resolves tenant
// ownership and records an immutable hint, not a state-machine transition.
func (s githubDeliveryStore) record(ctx context.Context, d githubWebhookDelivery) (string, error) {
	if s.db == nil || !githubDeliveryID.MatchString(d.deliveryID) || d.installationID <= 0 || d.repositoryID <= 0 {
		return "", errGitHubAuthorization
	}
	var result string
	err := s.db.QueryRowContext(ctx, `SELECT browseai.record_github_delivery($1,$2,$3,$4,$5)`,
		d.deliveryID, d.bodyDigest[:], d.installationID, d.repositoryID, d.event).Scan(&result)
	if err != nil {
		return "", errGitHubUnavailable
	}
	switch result {
	case "accepted", "duplicate", "rejected":
		return result, nil
	default:
		return "", errGitHubUnavailable
	}
}
