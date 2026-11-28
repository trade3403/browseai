package main

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func testGitHubCIRepair(t *testing.T, ctx context.Context, owner, writer *sql.DB, candidate githubCIFailureCandidate, clock func() time.Time) {
	t.Helper()
	tx, err := writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.record(ctx, tx, clock); err == nil {
		tx.Rollback()
		t.Fatal("writer can record failures")
	}
	tx.Rollback()
	for _, max := range []int{0, 1} {
		tx, err := owner.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		tenant, task := candidate.policy.binding.tenantID, candidate.task.taskID
		if _, err := tx.ExecContext(ctx, "UPDATE browseai.tasks SET max_repairs=$2 WHERE id=$1", task, max); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.stages(tenant_id,task_id,stage_id,ordinal,weight,instruction,acceptance) VALUES($1,$2,'stage',0,1,'repair fixture',ARRAY['CI'])", tenant, task); err != nil {
			t.Fatal(err)
		}
		var fence int64
		err = tx.QueryRowContext(ctx, "INSERT INTO browseai.leases(repository_id,code_branch,tenant_id,task_id,project_id,expires_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()+interval '60 seconds') RETURNING fence", candidate.policy.binding.repositoryID, candidate.policy.branch, tenant, task, candidate.task.projectID).Scan(&fence)
		if err != nil {
			t.Fatal(err)
		}
		stale := candidate
		stale.observedAt = clock().Add(-githubEvidenceLifetime)
		if _, err := stale.record(ctx, tx, clock); err == nil {
			t.Fatal("expired failure accepted")
		}
		id, err := candidate.record(ctx, tx, clock)
		if err != nil {
			t.Fatal("failure recording failed", err)
		}
		if _, err := candidate.record(ctx, tx, clock); err == nil {
			t.Fatal("failure replay accepted")
		}
		revision := uint64(candidate.task.revision + 1)
		if _, err := reserveGitHubCIRepair(ctx, tx, tenant, task, id, revision, fence+1); err == nil {
			t.Fatal("wrong fence accepted")
		}
		want := taskAwaitApproval
		if max == 0 {
			want = taskNeedHuman
		}
		if got, err := reserveGitHubCIRepair(ctx, tx, tenant, task, id, revision, fence); err != nil || got != want {
			t.Fatal("repair reservation failed", got, err)
		}
		if _, err := reserveGitHubCIRepair(ctx, tx, tenant, task, id, revision, fence); err == nil {
			t.Fatal("repair replay accepted")
		}
		var state string
		var repairs int
		var current int64
		if err := tx.QueryRowContext(ctx, "SELECT state,repairs,revision FROM browseai.tasks WHERE id=$1", task).Scan(&state, &repairs, &current); err != nil || state != string(want) || repairs != max || current != candidate.task.revision+2 {
			t.Fatal("unexpected repair state", state, repairs, current, err)
		}
		testGitHubRepairApproval(t, ctx, tx, tenant, task, id, current, fence, want, repairs)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}
