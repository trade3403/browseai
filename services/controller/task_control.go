package main

import (
	"context"
	"database/sql"
	"errors"
)

// Public resume can only reconcile local, never-dispatched first-stage drafts.
// Other recovery requires a trusted Git/browser adapter in a later phase.
func localResumeEvidence(ctx context.Context, tx *sql.Tx, p principal, r taskRecord) (taskEvidence, error) {
	if r.State != taskPaused || r.Suspended != taskQueued || r.Stage != 0 || r.CodeSHA != "" || r.Repairs != 0 {
		return taskEvidence{}, errResourceConflict
	}
	var clear bool
	err := tx.QueryRowContext(ctx, "SELECT NOT EXISTS(SELECT 1 FROM browseai.leases WHERE tenant_id=$1 AND task_id=$2) AND NOT EXISTS(SELECT 1 FROM browseai.audit_logs WHERE tenant_id=$1 AND resource_id=$2 AND action IN ('task.ready','task.prepare','task.confirm_sent','task.approve'))", p.tenantID, r.ID).Scan(&clear)
	if err != nil {
		return taskEvidence{}, err
	}
	if !clear {
		return taskEvidence{}, errResourceConflict
	}
	return taskEvidence{ManualResume: true, Reconciled: true}, nil
}

// An approval is only a recorded human intent, bound to the task revision,
// stage and live fence. It does not send anything or advance the state.
// Later dispatch must also bind the exact instruction and prove manual send.
func (s taskStore) approve(ctx context.Context, p principal, id string, revision uint64) (taskRecord, error) {
	return mutationTx(ctx, s.db, p, "task.approve", id, revision, nil, func(tx *sql.Tx) (taskRecord, error) {
		r, err := lockedTask(ctx, tx, p, id, revision)
		if err != nil {
			return taskRecord{}, err
		}
		if r.State != taskAwaitApproval {
			return taskRecord{}, errResourceConflict
		}
		var fence int64
		err = tx.QueryRowContext(ctx, "SELECT fence FROM browseai.leases WHERE tenant_id=$1 AND task_id=$2 AND expires_at>clock_timestamp() FOR UPDATE", p.tenantID, id).Scan(&fence)
		if errors.Is(err, sql.ErrNoRows) {
			return taskRecord{}, errResourceConflict
		}
		if err != nil {
			return taskRecord{}, err
		}
		res, err := tx.ExecContext(ctx, "INSERT INTO browseai.task_approvals(tenant_id,task_id,revision,stage_index,fence,actor_id) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING", p.tenantID, id, int64(revision), r.Stage, fence, p.userID)
		if err != nil {
			return taskRecord{}, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return taskRecord{}, err
		}
		if n != 1 {
			return taskRecord{}, errResourceConflict
		}
		return r, nil
	})
}

func requireTaskApproval(ctx context.Context, tx *sql.Tx, p principal, r taskRecord, fence int64) error {
	var approved bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM browseai.task_approvals WHERE tenant_id=$1 AND task_id=$2 AND revision=$3 AND stage_index=$4 AND fence=$5 AND actor_id=$6)", p.tenantID, r.ID, int64(r.Revision), r.Stage, fence, p.userID).Scan(&approved)
	if err != nil {
		return err
	}
	if !approved {
		return errResourceConflict
	}
	return nil
}
