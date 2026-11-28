package main

import (
	"context"
	"database/sql"
	"testing"
)

func testGitHubCITransition(t *testing.T, ctx context.Context, owner, writer, reader, operator *sql.DB, tenant string, task githubEvidenceTask, push string, pushDigest []byte, pr string, prDigest []byte) {
	t.Helper()
	const query = "SELECT browseai.accept_github_ci($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)"
	var workflow int64
	var source string
	if err := owner.QueryRowContext(ctx, "SELECT workflow_id,source_digest FROM browseai.github_ci_evidence WHERE id=$1", push).Scan(&workflow, &source); err != nil {
		t.Fatal(err)
	}
	const actor = "00000000-0000-4000-8000-000000009981"
	const approval = "00000000-0000-4000-8000-000000009982"
	args := []any{tenant, task.taskID, task.revision, actor, approval, push, pushDigest, pr, prDigest, workflow, source}
	for _, db := range []*sql.DB{writer, reader, operator} {
		var ok bool
		if err := db.QueryRowContext(ctx, query, args...).Scan(&ok); err == nil {
			t.Fatal("runtime role can accept CI")
		}
	}
	tx, err := owner.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	check := func(want bool, values []any) {
		t.Helper()
		var ok bool
		if err := tx.QueryRowContext(ctx, query, values...).Scan(&ok); err != nil || ok != want {
			t.Fatal("unexpected acceptance result", ok, err)
		}
	}
	check(false, args)
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.users(id,login,password_hash) VALUES($1,'ci-acceptance-fixture','synthetic')", actor); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.memberships(tenant_id,user_id,role) VALUES($1,$2,'platform_admin')", tenant, actor); err != nil {
		t.Fatal(err)
	}
	bad := append([]any(nil), args...)
	bad[8] = make([]byte, 32)
	check(false, bad)
	bad = append([]any(nil), args...)
	bad[7], bad[8] = push, pushDigest
	check(false, bad)
	// Force the second consume to fail after the first succeeds.
	if _, err := tx.ExecContext(ctx, "UPDATE browseai.github_ci_evidence SET observed_at=observed_at-interval '1 hour',issued_at=issued_at-interval '1 hour',expires_at=expires_at-interval '1 hour' WHERE id=$1", pr); err != nil {
		t.Fatal(err)
	}
	check(false, args)
	var consumed int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM browseai.github_ci_evidence WHERE id IN($1,$2) AND consumed_at IS NOT NULL", push, pr).Scan(&consumed); err != nil || consumed != 0 {
		t.Fatal("partial consumption", consumed, err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE browseai.github_ci_evidence SET observed_at=observed_at+interval '1 hour',issued_at=issued_at+interval '1 hour',expires_at=expires_at+interval '1 hour' WHERE id=$1", pr); err != nil {
		t.Fatal(err)
	}
	check(true, args)
	check(false, args)
	var state string
	var revision int64
	if err := tx.QueryRowContext(ctx, "SELECT state,revision FROM browseai.tasks WHERE id=$1", task.taskID).Scan(&state, &revision); err != nil || state != "STAGE_PASSED" || revision != task.revision+1 {
		t.Fatal("unexpected transition", state, revision, err)
	}
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM browseai.github_ci_evidence WHERE id IN($1,$2) AND consumed_at IS NOT NULL", push, pr).Scan(&consumed); err != nil || consumed != 2 {
		t.Fatal("evidence not consumed", consumed, err)
	}
	var audit int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM browseai.github_ci_acceptance c JOIN browseai.audit_logs a ON a.id=c.approval_id JOIN browseai.events e ON e.audit_id=a.id WHERE c.approval_id=$1 AND a.actor_id=$2 AND c.push_id=$3 AND c.pr_id=$4 AND a.action='task.ci_pass' AND e.kind=a.action AND e.revision=a.revision", approval, actor, push, pr).Scan(&audit); err != nil || audit != 1 {
		t.Fatal("missing acceptance audit", audit, err)
	}
	testGitHubStageDecision(t, ctx, tx, writer, tenant, task.taskID, approval, actor, uint64(revision))
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
