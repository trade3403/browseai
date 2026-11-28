package main

import (
	"context"
	"database/sql"
	"sync"
	"testing"
)

func TestGitHubMonitorSchedulePolicies(t *testing.T) {
	p := githubMonitorPolicyKey{"00000000-0000-4000-8000-000000000001", 12, 34, 56, "push"}
	q := p
	q.event = "pull_request"
	policies := map[githubMonitorPolicyKey][]string{p: {"gate", "backend"}, q: {"backend", "gate"}}
	got, err := githubMonitorSchedulePolicies(policies)
	if err != nil || len(got) != 1 || got[0] != p || policies[p][0] != "gate" {
		t.Fatal("valid pair rejected or mutated", got, err)
	}
	policies[q] = []string{"gate", "other"}
	if _, err := githubMonitorSchedulePolicies(policies); err == nil {
		t.Fatal("mismatched jobs accepted")
	}
	delete(policies, q)
	if _, err := githubMonitorSchedulePolicies(policies); err == nil {
		t.Fatal("unpaired policy accepted")
	}
}

func testGitHubMonitorScheduling(t *testing.T, ctx context.Context, owner, worker *sql.DB, tenant, task string) {
	t.Helper()
	// Status test advanced the fixture revision; old monitor must not be reused.
	var revision int64
	if err := owner.QueryRowContext(ctx, "SELECT revision FROM browseai.tasks WHERE id=$1", task).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := owner.ExecContext(ctx, "UPDATE browseai.tasks SET revision=revision+1 WHERE id=$1", task); err != nil {
			t.Error(err)
		}
	}()
	p := githubMonitorPolicyKey{tenant, 99202, 99201, 10, "push"}
	s := githubMonitorStore{worker}
	bad := p
	bad.installation++
	if count, err := s.schedulePair(ctx, bad); err != nil || count != 0 {
		t.Fatal("foreign installation scheduled", count, err)
	}
	type result struct {
		count int
		err   error
	}
	out := make(chan result, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := s.schedulePair(ctx, p)
			out <- result{n, err}
		}()
	}
	wg.Wait()
	close(out)
	total := 0
	for r := range out {
		if r.err != nil {
			t.Fatal(r.err)
		}
		total += r.count
	}
	if total != 2 {
		t.Fatal("pair scheduled more than once or incomplete", total)
	}
	if n, err := s.schedulePair(ctx, p); err != nil || n != 0 {
		t.Fatal("duplicate pair", n, err)
	}
	var count int
	if err := owner.QueryRowContext(ctx, "SELECT count(*) FROM browseai.github_monitors m JOIN browseai.tasks t ON t.id=m.task_id WHERE m.task_id=$1 AND m.task_revision=$2 AND m.code_sha=t.code_sha AND m.state='unknown' AND m.workflow_id=10 AND m.event_type IN ('push','pull_request')", task, revision).Scan(&count); err != nil || count != 2 {
		t.Fatal("incorrect pair", count, err)
	}
	if _, err := owner.ExecContext(ctx, "UPDATE browseai.github_monitors SET expires_at=clock_timestamp()-interval '1 second' WHERE task_id=$1 AND task_revision=$2", task, revision); err != nil {
		t.Fatal(err)
	}
	if n, err := s.schedulePair(ctx, p); err != nil || n != 0 {
		t.Fatal("expired pair renewed", n, err)
	}
	var allowed bool
	if err := owner.QueryRowContext(ctx, "SELECT has_function_privilege('browseai_app','browseai.schedule_github_monitor_pair(uuid,bigint,bigint,bigint)','EXECUTE')").Scan(&allowed); err != nil || allowed {
		t.Fatal("app can schedule arbitrary targets", allowed, err)
	}
}
