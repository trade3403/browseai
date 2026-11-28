package main

import (
	"context"
	"database/sql"
	"testing"
)

func testGitHubRepairApproval(t *testing.T, ctx context.Context, tx *sql.Tx, tenant, task, failure string, revision, fence int64, target taskState, repairs int) {
	t.Helper()
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM browseai.github_ci_repair_audit WHERE failure_id=$1 AND observer_role=session_user AND ((action='failure_recorded' AND task_revision=$2-1) OR (target_state=$3 AND task_revision=$2 AND repairs=$4))", failure, revision, string(target), repairs).Scan(&count); err != nil || count != 2 {
		t.Fatal("missing system audit", count, err)
	}
	const actor = "00000000-0000-4000-8000-000000009985"
	const approval = "00000000-0000-4000-8000-000000009986"
	const query = "SELECT browseai.approve_github_ci_repair($1,$2,$3,$4,$5,$6,$7,$8)"
	args := []any{tenant, task, revision, failure, actor, approval, fence, []byte("Review and repair failed CI")}
	check := func(want bool, values []any) {
		t.Helper()
		var ok bool
		if err := tx.QueryRowContext(ctx, query, values...).Scan(&ok); err != nil || ok != want {
			t.Fatal("unexpected approval result", ok, err)
		}
	}
	check(false, args)
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.users(id,login,password_hash) VALUES($1,'repair-approval-fixture','synthetic')", actor); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.memberships(tenant_id,user_id,role) VALUES($1,$2,'user')", tenant, actor); err != nil {
		t.Fatal(err)
	}
	check(false, args)
	if _, err := tx.ExecContext(ctx, "UPDATE browseai.memberships SET role='platform_admin' WHERE tenant_id=$1 AND user_id=$2", tenant, actor); err != nil {
		t.Fatal(err)
	}
	bad := append([]any(nil), args...)
	bad[6] = fence + 1
	check(false, bad)
	bad = append([]any(nil), args...)
	bad[7] = []byte{}
	check(false, bad)
	if target == taskNeedHuman {
		check(false, args)
		return
	}
	check(true, args)
	check(false, args)
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM browseai.github_ci_repair_approvals p JOIN browseai.audit_logs a ON a.id=p.approval_id JOIN browseai.events e ON e.audit_id=a.id WHERE p.failure_id=$1 AND p.task_revision=$2 AND p.fence=$3 AND p.instruction_digest=sha256($4::bytea) AND a.actor_id=$5 AND e.kind='task.ci_repair_approve'", failure, revision, fence, args[7], actor).Scan(&count); err != nil || count != 1 {
		t.Fatal("missing human approval audit", count, err)
	}
	var state string
	var current int64
	if err := tx.QueryRowContext(ctx, "SELECT state,revision FROM browseai.tasks WHERE id=$1", task).Scan(&state, &current); err != nil || state != "AWAIT_APPROVAL" || current != revision {
		t.Fatal("approval moved task", state, current, err)
	}
	testGitHubRepairConfirmation(t, ctx, tx, tenant, task, approval, actor, uint64(revision), fence, args[7].([]byte))
}
