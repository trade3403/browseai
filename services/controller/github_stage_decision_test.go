package main

import (
	"context"
	"database/sql"
	"testing"
)

func testGitHubStageDecision(t *testing.T, ctx context.Context, tx *sql.Tx, writer *sql.DB, tenant, task, acceptance, actor string, revision uint64) {
	t.Helper()
	const decision = "00000000-0000-4000-8000-000000009987"
	const query = "SELECT browseai.decide_github_stage($1,$2,$3,$4,$5,$6,$7)"
	var ok bool
	if err := writer.QueryRowContext(ctx, query, tenant, task, int64(revision), acceptance, actor, decision, "complete").Scan(&ok); err == nil {
		t.Fatal("writer can decide stage")
	}
	if err := decideGitHubStage(ctx, tx, tenant, task, acceptance, actor, decision, revision, actionComplete); err == nil {
		t.Fatal("incomplete stage plan accepted")
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.stages(tenant_id,task_id,stage_id,ordinal,weight,instruction,acceptance) VALUES($1,$2,'first',0,1,'fixture',ARRAY['CI'])", tenant, task); err != nil {
		t.Fatal(err)
	}
	if err := decideGitHubStage(ctx, tx, tenant, task, acceptance, actor, decision, revision, actionNext); err == nil {
		t.Fatal("next stage accepted at final stage")
	}
	if err := decideGitHubStage(ctx, tx, tenant, task, decision, actor, acceptance, revision, actionComplete); err == nil {
		t.Fatal("unrelated acceptance accepted")
	}
	if _, err := tx.ExecContext(ctx, "SAVEPOINT stage_final"); err != nil {
		t.Fatal(err)
	}
	if err := decideGitHubStage(ctx, tx, tenant, task, acceptance, actor, decision, revision, actionComplete); err != nil {
		t.Fatal("final stage decision failed", err)
	}
	if err := decideGitHubStage(ctx, tx, tenant, task, acceptance, actor, decision, revision, actionComplete); err == nil {
		t.Fatal("stage decision replayed")
	}
	var state string
	var current int64
	if err := tx.QueryRowContext(ctx, "SELECT state,revision FROM browseai.tasks WHERE id=$1", task).Scan(&state, &current); err != nil || state != "COMPLETED" || current != int64(revision)+1 {
		t.Fatal("unexpected final state", state, current, err)
	}
	if _, err := tx.ExecContext(ctx, "ROLLBACK TO SAVEPOINT stage_final"); err != nil {
		t.Fatal(err)
	}
	// A two-stage fixture exercises the non-final branch of the same
	// primitive. All modifications remain inside the owner rollback test.
	if _, err := tx.ExecContext(ctx, "UPDATE browseai.tasks SET stage_count=2,repairs=1 WHERE id=$1", task); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.stages(tenant_id,task_id,stage_id,ordinal,weight,instruction,dependencies,acceptance) VALUES($1,$2,'second',1,1,'fixture',ARRAY['first'],ARRAY['CI'])", tenant, task); err != nil {
		t.Fatal(err)
	}
	if err := decideGitHubStage(ctx, tx, tenant, task, acceptance, actor, decision, revision, actionComplete); err == nil {
		t.Fatal("premature completion accepted")
	}
	if err := decideGitHubStage(ctx, tx, tenant, task, acceptance, actor, decision, revision, actionNext); err != nil {
		t.Fatal("next stage failed", err)
	}
	var stage, repairs int
	if err := tx.QueryRowContext(ctx, "SELECT state,revision,stage_index,repairs FROM browseai.tasks WHERE id=$1", task).Scan(&state, &current, &stage, &repairs); err != nil || state != "QUEUED" || current != int64(revision)+1 || stage != 1 || repairs != 0 {
		t.Fatal("unexpected next state", state, current, stage, repairs, err)
	}
	var audit int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM browseai.github_stage_decisions d JOIN browseai.audit_logs a ON a.id=d.decision_id JOIN browseai.events e ON e.audit_id=a.id WHERE d.decision_id=$1 AND d.acceptance_id=$2 AND d.stage_index=0 AND d.decision='next_stage' AND a.actor_id=$3 AND e.kind='task.next_stage'", decision, acceptance, actor).Scan(&audit); err != nil || audit != 1 {
		t.Fatal("missing stage audit", audit, err)
	}
}
