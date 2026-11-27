package main

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPostgresGitHubMonitorStep(t *testing.T) {
	ownerDSN := os.Getenv("BROWSEAI_TEST_OWNER_DSN")
	monitorDSN := os.Getenv("BROWSEAI_TEST_MONITOR_DSN")
	if ownerDSN == "" && monitorDSN == "" {
		t.Skip("isolated PostgreSQL integration only")
	}
	if ownerDSN == "" || monitorDSN == "" {
		t.Fatal("incomplete monitor configuration")
	}
	open := func(dsn string) *sql.DB {
		t.Helper()
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	owner, worker := open(ownerDSN), open(monitorDSN)
	if err := githubMonitorReady(context.Background(), worker); err != nil {
		t.Fatal("monitor role rejected", err)
	}
	if err := githubMonitorReady(context.Background(), owner); err == nil {
		t.Fatal("owner accepted as monitor")
	}
	tenant := "00000000-0000-4000-8000-000000000001"
	project := "00000000-0000-4000-8000-000000009201"
	task := "00000000-0000-4000-8000-000000009202"
	sha := strings.Repeat("b", 40)
	if _, err := owner.Exec("INSERT INTO browseai.projects(id,tenant_id,name,repository_id,repository,code_branch) VALUES($1,$2,'monitor step',99201,'owner/monitor-step','feat/monitor')", project, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec("INSERT INTO browseai.tasks(id,tenant_id,project_id,name,state,stage_count,code_sha) VALUES($1,$2,$3,'monitor step','WAIT_CI',1,$4)", task, tenant, project, sha); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec("SELECT browseai.provision_github_binding($1,99202,99201,'owner/monitor-step',12,34)", tenant); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := worker.QueryRow("SELECT browseai.enqueue_github_monitor($1,$2,10,'push')::text", tenant, task).Scan(&id); err != nil {
		t.Fatal(err)
	}
	s := githubMonitorStore{worker}
	ctx := context.Background()
	type outcome struct {
		found bool
		err   error
	}
	results := make(chan outcome, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, found, err := s.claim(ctx)
			results <- outcome{found, err}
		}()
	}
	wg.Wait()
	close(results)
	winners := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.found {
			winners++
		}
	}
	if winners != 1 {
		t.Fatal("claim race", winners)
	}
	if _, err := owner.Exec("UPDATE browseai.github_monitors SET claim_until=clock_timestamp()-interval '1 second' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	read := func(ctx context.Context, m githubMonitorClaim, now time.Time) (githubCIState, error) {
		if m.binding.fullName != "owner/monitor-step" || m.binding.tenantID != tenant || m.sha != sha || m.workflowID != 10 {
			t.Fatal("wrong durable target")
		}
		return githubCIPassed, githubReadError{retryAt: now.Add(time.Hour)}
	}
	found, err := s.step(ctx, read)
	if err != nil || !found {
		t.Fatal("step failed", err)
	}
	var state string
	var failures int
	var delayed bool
	if err := owner.QueryRow("SELECT state,failures,next_at>clock_timestamp()+interval '50 minutes' FROM browseai.github_monitors WHERE id=$1", id).Scan(&state, &failures, &delayed); err != nil || state != "unknown" || failures != 1 || !delayed {
		t.Fatal("bad persisted backoff", state, failures, delayed, err)
	}
	if found, err := s.step(ctx, read); found || err != nil {
		t.Fatal("backoff bypassed", found, err)
	}
	if _, err := owner.Exec("UPDATE browseai.github_monitors SET next_at=clock_timestamp()-interval '1 second' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec("UPDATE browseai.github_installation_cooldowns SET next_at=clock_timestamp()-interval '1 second' WHERE installation_id=99202"); err != nil {
		t.Fatal(err)
	}
	found, err = s.step(ctx, func(context.Context, githubMonitorClaim, time.Time) (githubCIState, error) {
		return githubCIPending, errGitHubAuthorization
	})
	if err != nil || !found {
		t.Fatal("authorization stop failed", err)
	}
	if err := owner.QueryRow("SELECT failures FROM browseai.github_monitors WHERE id=$1", id).Scan(&failures); err != nil || failures != githubPollMaxFailures {
		t.Fatal("authorization stop not persisted", failures, err)
	}
	if _, err := worker.Exec("SELECT * FROM browseai.github_monitors"); err == nil {
		t.Fatal("direct monitor read granted")
	}
}
