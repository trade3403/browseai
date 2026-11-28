package main

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/lib/pq"
)

// Inspect effective privileges, including inherited roles and PUBLIC.
// No runtime identity may directly touch these sealed tables or functions.
func testGitHubSealedPrivileges(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	roles := []string{"browseai_app", "browseai_auth", "browseai_admin", "browseai_github_operator", "browseai_webhook", "browseai_monitor", "browseai_verifier_operator", "browseai_verifier_reader", "browseai_evidence_writer", "browseai_failure_observer", "browseai_failure_grant_operator", "browseai_ci_acceptor", "browseai_ci_approval_operator", "browseai_repair_reserver"}
	tables := []string{"github_ci_evidence", "github_ci_acceptance", "github_ci_failures", "github_ci_repair_audit", "github_ci_repair_approvals", "github_stage_decisions", "github_ci_repair_confirmations", "github_failure_observer_grants", "github_ci_acceptance_grants"}
	functions := []string{
		"consume_github_ci_evidence(uuid,uuid,bigint,uuid,bytea)",
		"accept_github_ci(uuid,uuid,bigint,uuid,uuid,uuid,bytea,uuid,bytea,bigint,text)",
		"record_github_ci_failure(uuid,uuid,bigint,uuid,bigint,bigint,text,text,bigint,text,text,bigint,bigint,bigint[],timestamptz)",
		"reserve_github_ci_repair(uuid,uuid,bigint,uuid,bigint)",
		"approve_github_ci_repair(uuid,uuid,bigint,uuid,uuid,uuid,bigint,bytea)",
		"decide_github_stage(uuid,uuid,bigint,uuid,uuid,uuid,text)",
		"confirm_github_ci_repair(uuid,uuid,bigint,uuid,uuid,uuid,bigint,bytea)",
	}
	for _, role := range roles {
		for _, table := range tables {
			var allowed bool
			err := db.QueryRowContext(ctx, "SELECT has_table_privilege($1,$2,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER') OR has_any_column_privilege($1,$2,'SELECT,INSERT,UPDATE,REFERENCES')", role, "browseai."+table).Scan(&allowed)
			if err != nil || allowed {
				t.Fatal("sealed table privilege", role, table, allowed, err)
			}
		}
		var reservationAllowed bool
		if err := db.QueryRowContext(ctx, "SELECT has_function_privilege($1,'browseai.reserve_authorized_github_repair(uuid,uuid,uuid,bigint,uuid,bigint)','EXECUTE')", role).Scan(&reservationAllowed); err != nil || reservationAllowed != (role == "browseai_repair_reserver") {
			t.Fatal("reservation privilege", role, reservationAllowed, err)
		}
		for _, function := range functions {
			var allowed bool
			err := db.QueryRowContext(ctx, "SELECT has_function_privilege($1,$2,'EXECUTE')", role, "browseai."+function).Scan(&allowed)
			if err != nil || allowed {
				t.Fatal("sealed function privilege", role, function, allowed, err)
			}
		}
	}
}

// Disposable owner fixture only: the preceding single-row consumption test
// intentionally marked Push consumed without advancing. Reset that synthetic
// mark to exercise a competing full acceptance and PR failure on the same
// task/revision. This reset is never available through the runtime.
func testGitHubOutcomeRace(t *testing.T, ctx context.Context, db *sql.DB, c githubCIFailureCandidate) {
	t.Helper()
	tenant, task := c.policy.binding.tenantID, c.task.taskID
	const actor = "00000000-0000-4000-8000-000000009974"
	const approval = "00000000-0000-4000-8000-000000009975"
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, "UPDATE browseai.github_ci_evidence SET consumed_at=NULL WHERE tenant_id=$1 AND task_id=$2 AND task_revision=$3 AND event_type='push' AND consumed_at IS NOT NULL", tenant, task, c.task.revision)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := res.RowsAffected(); err != nil || n != 1 {
		t.Fatal("expected one synthetic consumption mark", n, err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.users(id,login,password_hash) VALUES($1,'ci-outcome-fixture','synthetic')", actor); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.memberships(tenant_id,user_id,role) VALUES($1,$2,'platform_admin')", tenant, actor); err != nil {
		t.Fatal(err)
	}
	type evidence struct {
		id     string
		digest []byte
	}
	load := func(event string) evidence {
		t.Helper()
		var e evidence
		if err := tx.QueryRowContext(ctx, "SELECT id::text,evidence_digest FROM browseai.github_ci_evidence WHERE tenant_id=$1 AND task_id=$2 AND task_revision=$3 AND event_type=$4 AND consumed_at IS NULL", tenant, task, c.task.revision, event).Scan(&e.id, &e.digest); err != nil {
			t.Fatal(err)
		}
		return e
	}
	push, pr := load("push"), load("pull_request")
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	type result struct {
		pass bool
		ok   bool
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		pass := i%2 == 0
		wg.Add(1)
		go func(pass bool) {
			defer wg.Done()
			<-start
			var ok bool
			var err error
			if pass {
				err = db.QueryRowContext(ctx, "SELECT browseai.accept_github_ci($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)", tenant, task, c.task.revision, actor, approval, push.id, push.digest, pr.id, pr.digest, c.policy.workflowID, c.source).Scan(&ok)
			} else {
				var id sql.NullString
				err = db.QueryRowContext(ctx, "SELECT browseai.record_github_ci_failure($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)::text",
					tenant, task, c.task.revision, c.task.projectID, c.policy.binding.installationID, c.policy.binding.repositoryID, c.policy.branch, c.policy.sha,
					c.policy.workflowID, c.source, c.event, c.runID, c.attempt, pq.Array(c.failedIDs), c.observedAt).Scan(&id)
				ok = id.Valid
			}
			results <- result{pass, ok, err}
		}(pass)
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	passed := false
	for r := range results {
		if r.err != nil {
			t.Fatal("outcome race", r.err)
		}
		if r.ok {
			winners++
			passed = r.pass
		}
	}
	if winners != 1 {
		t.Fatal("expected exactly one outcome", winners)
	}
	var state string
	var revision int64
	var consumed, accepted, failed int
	err = db.QueryRowContext(ctx, "SELECT state,revision,(SELECT count(*) FROM browseai.github_ci_evidence WHERE task_id=$1 AND consumed_at IS NOT NULL),(SELECT count(*) FROM browseai.github_ci_acceptance WHERE task_id=$1),(SELECT count(*) FROM browseai.github_ci_failures WHERE task_id=$1) FROM browseai.tasks WHERE id=$1", task).Scan(&state, &revision, &consumed, &accepted, &failed)
	if err != nil || revision != c.task.revision+1 {
		t.Fatal("unexpected outcome revision", revision, err)
	}
	if passed {
		if state != "STAGE_PASSED" || consumed != 2 || accepted != 1 || failed != 0 {
			t.Fatal("mixed pass outcome", state, consumed, accepted, failed)
		}
	} else if state != "CI_FAILED" || consumed != 0 || accepted != 0 || failed != 1 {
		t.Fatal("mixed failure outcome", state, consumed, accepted, failed)
	}
}
