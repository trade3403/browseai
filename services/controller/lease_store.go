package main

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// A verifiedBranch must be constructed by trusted GitHub authorization code
// after checking the stable repository ID and exact ref. No public endpoint
// accepts it. The V0.3 GitHub adapter is not yet implemented.
type verifiedBranch struct {
	repositoryID int64
	branch       string
}

type branchLease struct {
	RepositoryID int64
	Branch       string
	TaskID       string
	Fence        int64
	ExpiresAt    time.Time
}

type leaseStore struct {
	db *sql.DB
}

const leaseFields = "repository_id,code_branch,task_id::text,fence,expires_at"

func readLease(row rowScanner) (branchLease, error) {
	var l branchLease
	err := row.Scan(&l.RepositoryID, &l.Branch, &l.TaskID, &l.Fence, &l.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return branchLease{}, errResourceConflict
	}
	return l, err
}

// A lease is deliberately not reassigned merely because it expires. An
// uncertain external writer must be reconciled before release.
func (s leaseStore) acquire(ctx context.Context, p principal, id string, revision uint64, target verifiedBranch) (branchLease, error) {
	if target.repositoryID <= 0 || target.branch == "" {
		return branchLease{}, errResourceInvalid
	}
	return tenantTx(ctx, s.db, p, func(tx *sql.Tx) (branchLease, error) {
		r, err := lockedTask(ctx, tx, p, id, revision)
		if err != nil {
			return branchLease{}, err
		}
		if r.State != taskQueued {
			return branchLease{}, errResourceConflict
		}
		var repo int64
		var branch string
		err = tx.QueryRowContext(ctx, "SELECT repository_id,code_branch FROM browseai.projects WHERE tenant_id=$1 AND id=$2 FOR KEY SHARE", p.tenantID, r.ProjectID).Scan(&repo, &branch)
		if err != nil {
			return branchLease{}, err
		}
		if repo != target.repositoryID || branch != target.branch {
			return branchLease{}, errResourceConflict
		}
		return readLease(tx.QueryRowContext(ctx, "INSERT INTO browseai.leases(repository_id,code_branch,tenant_id,task_id,project_id,expires_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()+interval '60 seconds') ON CONFLICT(repository_id,code_branch) DO NOTHING RETURNING "+leaseFields, repo, branch, p.tenantID, id, r.ProjectID))
	})
}

// Call inside the same transaction and after locking the task. Expiration
// uses the database clock, not the worker's potentially skewed clock.
func requireLiveLease(ctx context.Context, tx *sql.Tx, p principal, id string, fence int64) error {
	if fence <= 0 {
		return errResourceConflict
	}
	var found int64
	err := tx.QueryRowContext(ctx, "SELECT fence FROM browseai.leases WHERE tenant_id=$1 AND task_id=$2 AND fence=$3 AND expires_at>clock_timestamp() FOR UPDATE", p.tenantID, id, fence).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return errResourceConflict
	}
	return err
}

func (s leaseStore) renew(ctx context.Context, p principal, id string, revision uint64, fence int64) (branchLease, error) {
	return tenantTx(ctx, s.db, p, func(tx *sql.Tx) (branchLease, error) {
		r, err := lockedTask(ctx, tx, p, id, revision)
		if err != nil {
			return branchLease{}, err
		}
		if r.snapshot().terminal() || r.State == taskPaused || r.State == taskOffline || r.State == taskNeedHuman {
			return branchLease{}, errResourceConflict
		}
		if err := requireLiveLease(ctx, tx, p, id, fence); err != nil {
			return branchLease{}, err
		}
		return readLease(tx.QueryRowContext(ctx, "UPDATE browseai.leases SET expires_at=clock_timestamp()+interval '60 seconds' WHERE tenant_id=$1 AND task_id=$2 AND fence=$3 AND expires_at>clock_timestamp() RETURNING "+leaseFields, p.tenantID, id, fence))
	})
}

// Reconciliation is a trusted internal fact, never an HTTP flag. Releasing
// is allowed only while dispatch is inactive; pause/stop alone is not proof
// that an external writer has stopped. A stale token cannot release a new row.
func (s leaseStore) release(ctx context.Context, p principal, id string, revision uint64, fence int64, reconciled bool) error {
	if !reconciled || fence <= 0 {
		return errResourceConflict
	}
	_, err := tenantTx(ctx, s.db, p, func(tx *sql.Tx) (bool, error) {
		r, err := lockedTask(ctx, tx, p, id, revision)
		if err != nil {
			return false, err
		}
		switch r.State {
		case taskQueued, taskPaused, taskOffline, taskNeedHuman, taskCompleted, taskStopped:
		default:
			return false, errResourceConflict
		}
		res, err := tx.ExecContext(ctx, "DELETE FROM browseai.leases WHERE tenant_id=$1 AND task_id=$2 AND fence=$3", p.tenantID, id, fence)
		if err != nil {
			return false, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return false, err
		}
		if n != 1 {
			return false, errResourceConflict
		}
		return true, nil
	})
	return err
}
