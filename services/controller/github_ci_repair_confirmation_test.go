package main

import (
	"context"
	"database/sql"
	"testing"
)

func testGitHubRepairConfirmation(t *testing.T, ctx context.Context, tx *sql.Tx, tenant, task, approval, actor string, revision uint64, fence int64, instruction []byte) {
	t.Helper()
	const confirmation = "00000000-0000-4000-8000-000000009988"
	check := func(want bool, user string, version uint64, token int64, body []byte, sent bool) {
		t.Helper()
		err := confirmGitHubCIRepair(ctx, tx, tenant, task, approval, user, confirmation, version, token, body, sent)
		if (err == nil) != want {
			t.Fatal("unexpected confirmation", err)
		}
	}
	check(false, actor, revision, fence, instruction, false)
	check(false, actor, revision, fence, []byte("different instruction"), true)
	check(false, actor, revision+1, fence, instruction, true)
	check(false, actor, revision, fence+1, instruction, true)
	check(false, "00000000-0000-4000-8000-000000009989", revision, fence, instruction, true)
	if _, err := tx.ExecContext(ctx, "UPDATE browseai.users SET enabled=false WHERE id=$1", actor); err != nil {
		t.Fatal(err)
	}
	check(false, actor, revision, fence, instruction, true)
	if _, err := tx.ExecContext(ctx, "UPDATE browseai.users SET enabled=true WHERE id=$1", actor); err != nil {
		t.Fatal(err)
	}
	check(true, actor, revision, fence, instruction, true)
	check(false, actor, revision, fence, instruction, true)
	var state string
	var current int64
	var repairs int
	if err := tx.QueryRowContext(ctx, "SELECT state,revision,repairs FROM browseai.tasks WHERE id=$1", task).Scan(&state, &current, &repairs); err != nil || state != "DEVELOPING" || current != int64(revision)+1 || repairs != 1 {
		t.Fatal("unexpected confirmed state", state, current, repairs, err)
	}
	var count int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM browseai.github_ci_repair_confirmations c JOIN browseai.audit_logs a ON a.id=c.confirmation_id JOIN browseai.events e ON e.audit_id=a.id WHERE c.confirmation_id=$1 AND c.approval_id=$2 AND c.instruction_digest=sha256($3::bytea) AND a.actor_id=$4 AND e.kind='task.ci_repair_confirm_sent'", confirmation, approval, instruction, actor).Scan(&count); err != nil || count != 1 {
		t.Fatal("missing confirmation audit", count, err)
	}
}
