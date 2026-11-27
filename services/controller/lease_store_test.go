package main

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
)

func TestPostgresLeaseIntegration(t *testing.T) {
	ownerDSN, appDSN := os.Getenv("BROWSEAI_TEST_OWNER_DSN"), os.Getenv("BROWSEAI_TEST_APP_DSN")
	if ownerDSN == "" && appDSN == "" {
		t.Skip("isolated PostgreSQL integration only")
	}
	if ownerDSN == "" || appDSN == "" {
		t.Fatal("incomplete integration configuration")
	}
	open := func(dsn string) *sql.DB {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	owner, db := open(ownerDSN), open(appDSN)
	ctx := context.Background()
	a := principal{"00000000-0000-4000-8000-000000000031", "00000000-0000-4000-8000-000000000001", roleUser}
	b := principal{"00000000-0000-4000-8000-000000000032", "00000000-0000-4000-8000-000000000002", roleUser}
	projects := projectStore{db}
	tasks := taskStore{db}
	leases := leaseStore{db}
	target := verifiedBranch{789, "feat/lease-test"}
	create := func(p principal, branch string) taskRecord {
		project, err := projects.create(ctx, p, projectCreate{"Lease test", 789, "owner/lease-test", branch})
		if err != nil {
			t.Fatal(err)
		}
		r, err := tasks.create(ctx, p, taskCreate{ProjectID: project.ID, Name: "Lease task", Stages: []stageCreate{{"S1", 1}}})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	ra, rb := create(a, target.branch), create(b, target.branch)
	reject := func(err error) {
		t.Helper()
		if !errors.Is(err, errResourceConflict) {
			t.Fatal("expected conflict", err)
		}
	}
	_, err := leases.acquire(ctx, a, ra.ID, 0, verifiedBranch{790, target.branch})
	reject(err)
	_, err = tasks.transition(ctx, a, ra.ID, 0, actionReady, taskEvidence{ReadyToRun: true})
	reject(err)
	type attempt struct {
		p     principal
		task  taskRecord
		lease branchLease
		err   error
	}
	results := make(chan attempt, 2)
	for _, item := range []attempt{{p: a, task: ra}, {p: b, task: rb}} {
		go func(item attempt) {
			item.lease, item.err = leases.acquire(ctx, item.p, item.task.ID, 0, target)
			results <- item
		}(item)
	}
	first, second := <-results, <-results
	if first.err != nil {
		first, second = second, first
	}
	if first.err != nil || first.lease.Fence <= 0 {
		t.Fatal("no lease winner", first.err)
	}
	reject(second.err)
	p, r, l := first.p, first.task, first.lease
	_, err = leases.acquire(ctx, p, r.ID, 0, target)
	reject(err)
	_, err = leases.renew(ctx, p, r.ID, 0, l.Fence+1)
	reject(err)
	renewed, err := leases.renew(ctx, p, r.ID, 0, l.Fence)
	if err != nil || renewed.Fence != l.Fence || renewed.ExpiresAt.Before(l.ExpiresAt) {
		t.Fatal("renew failed", renewed, err)
	}
	ready, err := tasks.transition(ctx, p, r.ID, 0, actionReady, taskEvidence{ReadyToRun: true, LeaseFence: l.Fence})
	if err != nil || ready.State != taskReady {
		t.Fatal("ready without valid transition", ready, err)
	}
	reject(leases.release(ctx, p, r.ID, 1, l.Fence, true))
	// Simulate expiry using the DB owner, not local time or sleeping.
	if _, err := owner.Exec("UPDATE browseai.leases SET expires_at=clock_timestamp()-interval '1 second' WHERE task_id=$1", r.ID); err != nil {
		t.Fatal(err)
	}
	_, err = leases.renew(ctx, p, r.ID, 1, l.Fence)
	reject(err)
	_, err = tasks.transition(ctx, p, r.ID, 1, actionPrepare, taskEvidence{ReadyToRun: true, LeaseFence: l.Fence})
	reject(err)
	_, err = leases.acquire(ctx, second.p, second.task.ID, 0, target)
	reject(err)
	stopped, err := tasks.transition(ctx, p, r.ID, 1, actionStop, taskEvidence{})
	if err != nil || stopped.State != taskStopped {
		t.Fatal("stop failed", stopped, err)
	}
	reject(leases.release(ctx, p, r.ID, 2, l.Fence, false))
	if err := leases.release(ctx, p, r.ID, 2, l.Fence, true); err != nil {
		t.Fatal(err)
	}
	next, err := leases.acquire(ctx, second.p, second.task.ID, 0, target)
	if err != nil || next.Fence <= l.Fence {
		t.Fatal("fence did not advance", next, err)
	}
	reject(leases.release(ctx, second.p, second.task.ID, 0, l.Fence, true))
	_, err = tasks.transition(ctx, second.p, second.task.ID, 0, actionReady, taskEvidence{ReadyToRun: true, LeaseFence: l.Fence})
	reject(err)
	// Different branch may run concurrently.
	other := create(p, "feat/lease-other")
	parallel, err := leases.acquire(ctx, p, other.ID, 0, verifiedBranch{789, "feat/lease-other"})
	if err != nil || parallel.Fence <= next.Fence {
		t.Fatal("independent branch denied", parallel, err)
	}
	if err := leases.release(ctx, p, other.ID, 0, parallel.Fence, true); err != nil {
		t.Fatal(err)
	}
	if err := leases.release(ctx, second.p, second.task.ID, 0, next.Fence, true); err != nil {
		t.Fatal(err)
	}
}
