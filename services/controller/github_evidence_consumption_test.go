package main

import (
	"context"
	"database/sql"
	"sync"
	"testing"
)

// Only owner-side integration tests can invoke the sealed consumption primitive.
// No runtime login receives execution privileges until the state machine is wired.
func testGitHubEvidenceConsumption(t *testing.T, ctx context.Context, owner, writer, reader, operator *sql.DB, tenant string, task githubEvidenceTask) {
	t.Helper()
	type item struct {
		id     string
		digest []byte
	}
	load := func(event string) item {
		t.Helper()
		var v item
		if err := owner.QueryRowContext(ctx, "SELECT id::text,evidence_digest FROM browseai.github_ci_evidence WHERE tenant_id=$1 AND task_id=$2 AND event_type=$3", tenant, task.taskID, event).Scan(&v.id, &v.digest); err != nil {
			t.Fatal(err)
		}
		return v
	}
	push, pr := load("push"), load("pull_request")
	const query = "SELECT browseai.consume_github_ci_evidence($1,$2,$3,$4,$5)"
	check := func(db *sql.DB, tenantID string, revision int64, v item, want bool) {
		t.Helper()
		var ok bool
		if err := db.QueryRowContext(ctx, query, tenantID, task.taskID, revision, v.id, v.digest).Scan(&ok); err != nil || ok != want {
			t.Fatal("unexpected consume result", ok, err)
		}
	}
	for _, db := range []*sql.DB{writer, reader, operator} {
		var ok bool
		if err := db.QueryRowContext(ctx, query, tenant, task.taskID, task.revision, push.id, push.digest).Scan(&ok); err == nil {
			t.Fatal("runtime role can consume evidence")
		}
	}
	check(owner, "00000000-0000-4000-8000-000000000002", task.revision, push, false)
	check(owner, tenant, task.revision+1, push, false)
	check(owner, tenant, task.revision, item{push.id, make([]byte, 32)}, false)
	// Rollback must undo the mark, not strand a future task transition.
	tx, err := owner.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var ok bool
	if err := tx.QueryRowContext(ctx, query, tenant, task.taskID, task.revision, push.id, push.digest).Scan(&ok); err != nil || !ok {
		t.Fatal("transactional consume rejected", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	// Eight independent database calls compete for the same row.
	var wg sync.WaitGroup
	type result struct {
		ok  bool
		err error
	}
	results := make(chan result, 8)
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			var ok bool
			err := owner.QueryRowContext(ctx, query, tenant, task.taskID, task.revision, push.id, push.digest).Scan(&ok)
			results <- result{ok, err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if r.ok {
			winners++
		}
	}
	if winners != 1 {
		t.Fatal("expected one consumption", winners)
	}
	check(owner, tenant, task.revision, push, false)
	// Use rollback-only owner changes to isolate each invalidation case.
	for _, mutation := range []string{
		"UPDATE browseai.tasks SET revision=revision+1 WHERE id=(SELECT task_id FROM browseai.github_ci_evidence WHERE id=$1)",
		"UPDATE browseai.github_installations SET enabled=false WHERE installation_id=(SELECT installation_id FROM browseai.github_ci_evidence WHERE id=$1)",
		"UPDATE browseai.github_repositories SET enabled=false WHERE (tenant_id,repository_id)=(SELECT tenant_id,repository_id FROM browseai.github_ci_evidence WHERE id=$1)",
		"UPDATE browseai.github_verifier_keys SET revoked_at=clock_timestamp() WHERE key_id=(SELECT key_id FROM browseai.github_ci_evidence WHERE id=$1)",
		"UPDATE browseai.github_ci_evidence SET observed_at=observed_at-interval '1 hour',issued_at=issued_at-interval '1 hour',expires_at=expires_at-interval '1 hour' WHERE id=$1",
	} {
		tx, err := owner.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, mutation, pr.id); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
		err = tx.QueryRowContext(ctx, query, tenant, task.taskID, task.revision, pr.id, pr.digest).Scan(&ok)
		rollbackErr := tx.Rollback()
		if err != nil || rollbackErr != nil || ok {
			t.Fatal("invalid evidence consumed", mutation, ok, err, rollbackErr)
		}
	}
	// Consumption alone never changes task state or revision.
	var state string
	var revision int64
	if err := owner.QueryRowContext(ctx, "SELECT state,revision FROM browseai.tasks WHERE id=$1", task.taskID).Scan(&state, &revision); err != nil || state != "WAIT_CI" || revision != task.revision {
		t.Fatal("unexpected state change", state, revision, err)
	}
}
