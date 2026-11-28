package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
)

// The pure state machine remains reusable, but its legacy boolean evidence
// must not directly authorize any of the sealed persistence transitions.
func TestLegacyGitHubTransitionsDenied(t *testing.T) {
	for _, action := range []taskAction{actionCIPass, actionCIFail, actionRepair, actionNext, actionComplete} {
		if _, err := (taskStore{}).transition(context.Background(), principal{}, "", 0, action, taskEvidence{
			CIComplete: true, CIAllPassed: true, Acceptance: true, ReadyToRun: true, FinalAccepted: true,
		}); !errors.Is(err, errResourceConflict) {
			t.Fatal("legacy transition not blocked", action, err)
		}
	}
}

// Real PostgreSQL connections race the same immutable input. All successful
// calls commit, so a later round observes the actual durable winner.
func testGitHubRepairRace(t *testing.T, ctx context.Context, db *sql.DB, c githubCIFailureCandidate) {
	t.Helper()
	tenant, task := c.policy.binding.tenantID, c.task.taskID
	const actor = "00000000-0000-4000-8000-000000009971"
	const approval = "00000000-0000-4000-8000-000000009972"
	const confirmation = "00000000-0000-4000-8000-000000009973"
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.tasks(id,tenant_id,project_id,name,state,stage_count,code_sha,revision,max_repairs) VALUES($1,$2,$3,'repair race fixture','WAIT_CI',1,$4,$5,1)", task, tenant, c.task.projectID, c.policy.sha, c.task.revision); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.stages(tenant_id,task_id,stage_id,ordinal,weight,instruction,acceptance) VALUES($1,$2,'stage',0,1,'fixture',ARRAY['CI'])", tenant, task); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.users(id,login,password_hash) VALUES($1,'ci-race-fixture','synthetic')", actor); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.memberships(tenant_id,user_id,role) VALUES($1,$2,'platform_admin')", tenant, actor); err != nil {
		t.Fatal(err)
	}
	var fence int64
	if err := tx.QueryRowContext(ctx, "INSERT INTO browseai.leases(repository_id,code_branch,tenant_id,task_id,project_id,expires_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()+interval '60 seconds') RETURNING fence", c.policy.binding.repositoryID, c.policy.branch, tenant, task, c.task.projectID).Scan(&fence); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	grant := testGitHubFailureGrantOperator(t, ctx, db, c)
	observerDSN := os.Getenv("BROWSEAI_TEST_FAILURE_OBSERVER_DSN")
	if observerDSN == "" {
		t.Fatal("missing observer fixture")
	}
	observer, err := sql.Open("postgres", observerDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	if err := githubFailureObserverReady(ctx, observer); err != nil {
		t.Fatal("observer role rejected", err)
	}
	if err := githubFailureObserverReady(ctx, db); err == nil {
		t.Fatal("owner accepted as observer")
	}
	var denied sql.NullString
	if err := observer.QueryRowContext(ctx, "SELECT browseai.record_github_ci_failure($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::bigint[],$15)::text", tenant, task, c.task.revision, c.task.projectID, c.policy.binding.installationID, c.policy.binding.repositoryID, c.policy.branch, c.policy.sha, c.policy.workflowID, c.source, c.event, c.runID, c.attempt, "{1}", c.observedAt).Scan(&denied); err == nil {
		t.Fatal("observer can bypass grant")
	}
	defer func() {
		if _, err := db.ExecContext(ctx, "DELETE FROM browseai.leases WHERE tenant_id=$1 AND task_id=$2 AND fence=$3", tenant, task, fence); err != nil {
			t.Error("fixture lease cleanup", err)
		}
	}()
	race := func(name string, call func() (bool, error)) {
		t.Helper()
		type result struct {
			ok  bool
			err error
		}
		start := make(chan struct{})
		out := make(chan result, 8)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				ok, err := call()
				out <- result{ok, err}
			}()
		}
		close(start)
		wg.Wait()
		close(out)
		winners := 0
		for r := range out {
			if r.err != nil {
				t.Fatal(name, r.err)
			}
			if r.ok {
				winners++
			}
		}
		if winners != 1 {
			t.Fatal(name, "expected exactly one winner", winners)
		}
	}
	// Reject a wrong grant and a different pinned workflow before racing.
	if _, err := recordAuthorizedGitHubFailure(ctx, observer, c, "00000000-0000-4000-8000-000000009999", func() time.Time { return c.observedAt }); err == nil {
		t.Fatal("unknown grant accepted")
	}
	changed := c
	changed.policy.workflowID++
	if _, err := recordAuthorizedGitHubFailure(ctx, observer, changed, grant, func() time.Time { return c.observedAt }); err == nil {
		t.Fatal("changed workflow accepted")
	}
	race("record authorized failure", func() (bool, error) {
		var id sql.NullString
		err := observer.QueryRowContext(ctx, githubAuthorizedFailureSQL, grant, tenant, task, c.task.revision, c.task.projectID,
			c.policy.binding.installationID, c.policy.binding.repositoryID, c.policy.branch, c.policy.sha,
			c.policy.workflowID, c.source, c.event, c.runID, c.attempt, pq.Array(c.failedIDs), c.observedAt).Scan(&id)
		return id.Valid, err
	})
	var failure string
	if err := db.QueryRowContext(ctx, "SELECT id::text FROM browseai.github_ci_failures WHERE tenant_id=$1 AND task_id=$2", tenant, task).Scan(&failure); err != nil {
		t.Fatal(err)
	}
	var linked bool
	if err := db.QueryRowContext(ctx, "SELECT failure_id=$2 FROM browseai.github_failure_observer_grants WHERE id=$1", grant, failure).Scan(&linked); err != nil || !linked {
		t.Fatal("grant consumption missing", linked, err)
	}
	race("reserve repair", func() (bool, error) {
		var target sql.NullString
		err := db.QueryRowContext(ctx, "SELECT browseai.reserve_github_ci_repair($1,$2,$3,$4,$5)", tenant, task, c.task.revision+1, failure, fence).Scan(&target)
		if err == nil && target.Valid && target.String != "AWAIT_APPROVAL" {
			return false, errors.New("unexpected repair target")
		}
		return target.Valid, err
	})
	instruction := []byte("Review and repair race fixture")
	race("approve repair", func() (bool, error) {
		var ok bool
		err := db.QueryRowContext(ctx, "SELECT browseai.approve_github_ci_repair($1,$2,$3,$4,$5,$6,$7,$8)",
			tenant, task, c.task.revision+2, failure, actor, approval, fence, instruction).Scan(&ok)
		return ok, err
	})
	race("confirm repair", func() (bool, error) {
		var ok bool
		err := db.QueryRowContext(ctx, "SELECT browseai.confirm_github_ci_repair($1,$2,$3,$4,$5,$6,$7,$8)",
			tenant, task, c.task.revision+2, approval, actor, confirmation, fence, instruction).Scan(&ok)
		return ok, err
	})
	var state string
	var revision int64
	var repairs, audit, approvals, confirmations int
	err = db.QueryRowContext(ctx, "SELECT state,revision,repairs,(SELECT count(*) FROM browseai.github_ci_repair_audit WHERE failure_id=$2),(SELECT count(*) FROM browseai.github_ci_repair_approvals WHERE failure_id=$2),(SELECT count(*) FROM browseai.github_ci_repair_confirmations WHERE approval_id=$3) FROM browseai.tasks WHERE id=$1", task, failure, approval).Scan(&state, &revision, &repairs, &audit, &approvals, &confirmations)
	if err != nil || state != "DEVELOPING" || revision != c.task.revision+3 || repairs != 1 || audit != 2 || approvals != 1 || confirmations != 1 {
		t.Fatal("durable race result", state, revision, repairs, audit, approvals, confirmations, err)
	}
}
