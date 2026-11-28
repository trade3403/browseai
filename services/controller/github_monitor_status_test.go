package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
)

func testGitHubMonitorStatus(t *testing.T, ctx context.Context, owner, worker *sql.DB, tenant, task, monitor string) {
	t.Helper()
	dsn := os.Getenv("BROWSEAI_TEST_APP_DSN")
	if dsn == "" {
		t.Fatal("missing app fixture")
	}
	app, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	const actor = "00000000-0000-4000-8000-000000009203"
	if _, err := owner.ExecContext(ctx, "INSERT INTO browseai.users(id,login,password_hash) VALUES($1,'monitor-status-fixture','synthetic')", actor); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ExecContext(ctx, "INSERT INTO browseai.memberships(tenant_id,user_id,role) VALUES($1,$2,'user')", tenant, actor); err != nil {
		t.Fatal(err)
	}
	p := principal{actor, tenant, roleUser}
	store := taskStore{app}
	check := func(want githubCIState) {
		t.Helper()
		out, err := store.githubCIStatus(ctx, p, task)
		if err != nil || !out.ObservationOnly || len(out.Targets) != 1 || out.Targets[0].State != want || out.Targets[0].WorkflowID != 10 || out.Targets[0].Event != "push" {
			t.Fatal("unexpected monitor status", out, err)
		}
	}
	check(githubCIUnknown)
	if _, err := owner.ExecContext(ctx, "UPDATE browseai.github_monitors SET state='passed',failures=0,observed_at=clock_timestamp(),next_at=clock_timestamp()+interval '30 seconds' WHERE id=$1", monitor); err != nil {
		t.Fatal(err)
	}
	check(githubCIPassed)
	if _, err := owner.ExecContext(ctx, "UPDATE browseai.github_monitors SET observed_at=clock_timestamp()-interval '61 seconds' WHERE id=$1", monitor); err != nil {
		t.Fatal(err)
	}
	check(githubCIUnknown)
	if _, err := owner.ExecContext(ctx, "UPDATE browseai.github_monitors SET observed_at=clock_timestamp(),claim_token=gen_random_uuid(),claim_until=clock_timestamp()+interval '60 seconds' WHERE id=$1", monitor); err != nil {
		t.Fatal(err)
	}
	check(githubCIUnknown)
	if _, err := owner.ExecContext(ctx, "UPDATE browseai.github_monitors SET claim_token=NULL,claim_until=NULL,state='unknown',failures=8 WHERE id=$1", monitor); err != nil {
		t.Fatal(err)
	}
	other := principal{"00000000-0000-4000-8000-000000000032", "00000000-0000-4000-8000-000000000002", roleUser}
	if _, err := store.githubCIStatus(ctx, other, task); !errors.Is(err, errResourceMissing) {
		t.Fatal("cross-tenant status visible", err)
	}
	if _, err := owner.ExecContext(ctx, "UPDATE browseai.tasks SET revision=revision+1 WHERE id=$1", task); err != nil {
		t.Fatal(err)
	}
	if out, err := store.githubCIStatus(ctx, p, task); err != nil || len(out.Targets) != 0 {
		t.Fatal("old revision visible", out, err)
	}
	if _, err := app.ExecContext(ctx, "SELECT * FROM browseai.github_monitors"); err == nil {
		t.Fatal("direct monitor read granted")
	}
	if _, err := worker.ExecContext(ctx, "SELECT * FROM browseai.github_task_ci_status($1)", task); err == nil {
		t.Fatal("worker has presentation function grant")
	}
}
