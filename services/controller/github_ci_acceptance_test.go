package main

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func testGitHubCIService(t *testing.T, ctx context.Context, owner, writer *sql.DB, s githubVerifierStore, pushApp, prApp, badApp *githubApp, policy githubIndependentPolicy, clock func() time.Time) {
	t.Helper()
	load := func(event string) (string, githubVerifiedTaskEvidence) {
		t.Helper()
		var id string
		var v githubVerifiedTaskEvidence
		var digest []byte
		err := owner.QueryRowContext(ctx, "SELECT id::text,statement,signature,evidence_digest,observed_at,issued_at,expires_at FROM browseai.github_ci_evidence WHERE tenant_id=$1 AND task_id=$2 AND event_type=$3 AND consumed_at IS NULL",
			policy.execution.binding.tenantID, policy.task.taskID, event).Scan(&id, &v.statement, &v.signature, &digest, &v.observed, &v.issued, &v.expires)
		if err != nil || len(digest) != 32 {
			t.Fatal("could not load original evidence", err)
		}
		copy(v.digest[:], digest)
		return id, v
	}
	pushID, push := load("push")
	prID, pr := load("pull_request")
	approval := githubCIAcceptanceApproval{}
	approval.actorID = "00000000-0000-4000-8000-000000009983"
	approval.approvalID = "00000000-0000-4000-8000-000000009984"
	approval.tenantID, approval.task = policy.execution.binding.tenantID, policy.task
	approval.pushID, approval.prID = pushID, prID
	approval.pushDigest, approval.prDigest = push.digest, pr.digest
	bad := approval
	bad.prDigest = push.digest
	if _, err := s.prepareCIAcceptance(ctx, pushApp, prApp, policy, bad, push, pr, clock); err == nil {
		t.Fatal("approval for different evidence accepted")
	}
	if _, err := s.prepareCIAcceptance(ctx, pushApp, badApp, policy, approval, push, pr, clock); err == nil {
		t.Fatal("changed PR source accepted")
	}
	plan, err := s.prepareCIAcceptance(ctx, pushApp, prApp, policy, approval, push, pr, clock)
	if err != nil {
		t.Fatal("valid dual evidence rejected", err)
	}
	tx, err := writer.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.apply(ctx, tx, clock); err == nil {
		tx.Rollback()
		t.Fatal("writer can invoke sealed acceptance")
	}
	tx.Rollback()
	tx, err = owner.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := plan.apply(ctx, tx, clock); err == nil {
		t.Fatal("unavailable approver accepted")
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.users(id,login,password_hash) VALUES($1,'ci-service-fixture','synthetic')", approval.actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.memberships(tenant_id,user_id,role) VALUES($1,$2,'platform_admin')", approval.tenantID, approval.actorID); err != nil {
		t.Fatal(err)
	}
	if err := plan.apply(ctx, tx, func() time.Time { return plan.expires }); err == nil {
		t.Fatal("expired acceptance plan applied")
	}
	if err := plan.apply(ctx, tx, clock); err != nil {
		t.Fatal("sealed acceptance failed", err)
	}
	if err := plan.apply(ctx, tx, clock); err == nil {
		t.Fatal("acceptance replayed")
	}
	var state string
	var revision int64
	if err := tx.QueryRowContext(ctx, "SELECT state,revision FROM browseai.tasks WHERE id=$1", policy.task.taskID).Scan(&state, &revision); err != nil || state != "STAGE_PASSED" || revision != policy.task.revision+1 {
		t.Fatal("unexpected state", state, revision, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
