package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type githubMonitorClaim struct {
	id         string
	token      string
	binding    githubBinding
	branch     string
	sha        string
	workflowID int64
	event      string
	failures   int
	until      time.Time
	expires    time.Time
}

type githubMonitorStore struct {
	db *sql.DB
}

func (s githubMonitorStore) claim(ctx context.Context) (githubMonitorClaim, bool, error) {
	var m githubMonitorClaim
	err := s.db.QueryRowContext(ctx, `SELECT id::text,claim_token::text,tenant_id::text,installation_id,repository_id,
code_branch,code_sha,workflow_id,event_type,failures,claim_until,expires_at
FROM browseai.claim_github_monitor()`).Scan(&m.id, &m.token, &m.binding.tenantID, &m.binding.installationID,
		&m.binding.repositoryID, &m.branch, &m.sha, &m.workflowID, &m.event, &m.failures, &m.until, &m.expires)
	if errors.Is(err, sql.ErrNoRows) {
		return m, false, nil
	}
	if err != nil {
		return m, false, errGitHubUnavailable
	}
	return m, true, nil
}

func (s githubMonitorStore) repository(ctx context.Context, m githubMonitorClaim) (string, error) {
	var name sql.NullString
	if err := s.db.QueryRowContext(ctx, "SELECT browseai.github_monitor_repository($1,$2)", m.id, m.token).Scan(&name); err != nil {
		return "", errGitHubUnavailable
	}
	if !name.Valid || !repoName.MatchString(name.String) {
		return "", errGitHubAuthorization
	}
	return name.String, nil
}

func (s githubMonitorStore) publish(ctx context.Context, m githubMonitorClaim, result githubPollResult) error {
	var ok bool
	err := s.db.QueryRowContext(ctx, "SELECT browseai.publish_github_monitor($1,$2,$3,$4,$5)", m.id, m.token, string(result.state), result.failures, result.nextAt).Scan(&ok)
	if err != nil {
		return errGitHubUnavailable
	}
	if !ok {
		return errGitHubAuthorization
	}
	return nil
}

type githubMonitorReader func(context.Context, githubMonitorClaim, time.Time) (githubCIState, error)

// Execute at most one due claim. A caller must provide a trusted workflow/job
// policy through read; this layer does not promote a candidate to task success.
// No database transaction is held over remote I/O.
func (s githubMonitorStore) step(ctx context.Context, read githubMonitorReader) (bool, error) {
	if s.db == nil || read == nil {
		return false, errGitHubAuthorization
	}
	dbCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	m, found, err := s.claim(dbCtx)
	cancel()
	if err != nil || !found {
		return found, err
	}
	now := time.Now()
	deadline := now.Add(githubPollReadTimeout)
	for _, end := range []time.Time{m.until.Add(-5 * time.Second), m.expires.Add(-5 * time.Second)} {
		if end.Before(deadline) {
			deadline = end
		}
	}
	attempt, cancel := context.WithDeadline(ctx, deadline)
	name, readErr := s.repository(attempt, m)
	state := githubCIUnknown
	if readErr == nil {
		m.binding.fullName = name
		state, readErr = read(attempt, m, now)
	}
	if attempt.Err() != nil {
		readErr = attempt.Err()
	}
	cancel()
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	result := nextGitHubPoll(githubPollResult{failures: m.failures}, state, readErr, time.Now())
	if errors.Is(readErr, errGitHubAuthorization) {
		result.failures = githubPollMaxFailures
	}
	dbCtx, cancel = context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := s.publish(dbCtx, m, result); err != nil {
		return true, err
	}
	return true, nil
}
