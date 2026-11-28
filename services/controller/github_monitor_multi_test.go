package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPostgresGitHubMonitorMultipleTargets(t *testing.T) {
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
		db.SetMaxOpenConns(1)
		return db
	}
	owner := open(ownerDSN)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := owner.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	const tenant = "00000000-0000-4000-8000-000000000001"
	const otherTenant = "00000000-0000-4000-8000-000000000002"
	sha := strings.Repeat("c", 40)
	// Two repositories in one installation and a separate tenant/installation.
	for n := 0; n < 3; n++ {
		tid, installation := tenant, int64(99410)
		if n == 2 {
			tid, installation = otherTenant, 99411
		}
		repository := int64(99420 + n)
		project := fmt.Sprintf("00000000-0000-4000-8000-%012d", 9400+n)
		task := fmt.Sprintf("00000000-0000-4000-8000-%012d", 9410+n)
		name := fmt.Sprintf("owner/monitor-multi-%d", n)
		exec("INSERT INTO browseai.projects(id,tenant_id,name,repository_id,repository,code_branch) VALUES($1,$2,'multi monitor',$3,$4,'feat/multi')", project, tid, repository, name)
		exec("INSERT INTO browseai.tasks(id,tenant_id,project_id,name,state,stage_count,code_sha) VALUES($1,$2,$3,'multi monitor','WAIT_CI',1,$4)", task, tid, project, sha)
		exec("SELECT browseai.provision_github_binding($1,$2,$3,$4,12,34)", tid, installation, repository, name)
		var id sql.NullString
		if err := owner.QueryRowContext(ctx, "SELECT browseai.enqueue_github_monitor($1,$2,10,'push')::text", tid, task).Scan(&id); err != nil || !id.Valid {
			t.Fatal("fixture enqueue failed", err)
		}
	}
	type outcome struct {
		claim githubMonitorClaim
		found bool
		err   error
	}
	stores := make([]githubMonitorStore, 12)
	for i := range stores {
		stores[i] = githubMonitorStore{open(monitorDSN)}
	}
	race := func() []githubMonitorClaim {
		t.Helper()
		start := make(chan struct{})
		results := make(chan outcome, len(stores))
		var wg sync.WaitGroup
		for _, store := range stores {
			wg.Add(1)
			go func(s githubMonitorStore) {
				defer wg.Done()
				<-start
				m, found, err := s.claim(ctx)
				results <- outcome{m, found, err}
			}(store)
		}
		close(start)
		wg.Wait()
		close(results)
		var claims []githubMonitorClaim
		for result := range results {
			if result.err != nil {
				t.Fatal(result.err)
			}
			if result.found {
				claims = append(claims, result.claim)
			}
		}
		return claims
	}
	claims := race()
	if len(claims) != 2 {
		t.Fatal("expected one claim per installation", len(claims))
	}
	var shared, independent githubMonitorClaim
	for _, m := range claims {
		switch m.binding.installationID {
		case 99410:
			shared = m
			if m.binding.tenantID != tenant {
				t.Fatal("shared tenant mismatch")
			}
		case 99411:
			independent = m
			if m.binding.tenantID != otherTenant {
				t.Fatal("independent tenant mismatch")
			}
		default:
			t.Fatal("unexpected installation")
		}
	}
	if shared.id == "" || independent.id == "" {
		t.Fatal("installation isolation failed")
	}
	s := stores[0]
	now := time.Now()
	limited := nextGitHubPoll(githubPollResult{}, githubCIUnknown, githubReadError{retryAt: now.Add(time.Hour)}, now)
	if err := s.publish(ctx, shared, limited); err != nil {
		t.Fatal(err)
	}
	// A shared cooldown cannot prevent unrelated installations from polling.
	if err := s.publish(ctx, independent, nextGitHubPoll(githubPollResult{}, githubCIPending, nil, time.Now())); err != nil {
		t.Fatal(err)
	}
	exec("UPDATE browseai.github_monitors SET next_at=clock_timestamp()-interval '1 second' WHERE installation_id=99411")
	exec("UPDATE browseai.github_installation_cooldowns SET next_at=clock_timestamp()-interval '1 second' WHERE installation_id=99411")
	claims = race()
	if len(claims) != 1 || claims[0].binding.installationID != 99411 {
		t.Fatal("cooldown isolation failed", len(claims))
	}
	old := claims[0]
	// Simulate process death and a new worker recovering its expired lease.
	exec("UPDATE browseai.github_monitors SET claim_until=clock_timestamp()-interval '1 second' WHERE id=$1", old.id)
	claims = race()
	if len(claims) != 1 || claims[0].id != old.id || claims[0].token == old.token {
		t.Fatal("expired lease recovery failed")
	}
	fresh := claims[0]
	result := nextGitHubPoll(githubPollResult{}, githubCIPassed, nil, time.Now())
	if err := s.publish(ctx, old, result); err == nil {
		t.Fatal("expired worker published")
	}
	if _, err := s.repository(ctx, old); err == nil {
		t.Fatal("expired worker resolved repository")
	}
	exec("UPDATE browseai.github_installations SET enabled=false WHERE installation_id=99411")
	if _, err := s.repository(ctx, fresh); err == nil {
		t.Fatal("revoked binding resolved")
	}
	if err := s.publish(ctx, fresh, result); err == nil {
		t.Fatal("revoked worker published")
	}
	// Explicitly advance the test clock state, never sleep for real cooldown.
	exec("UPDATE browseai.github_installation_cooldowns SET next_at=clock_timestamp()-interval '1 second' WHERE installation_id=99410")
	claims = race()
	if len(claims) != 1 || claims[0].binding.installationID != 99410 || claims[0].id == shared.id {
		t.Fatal("other target did not recover after shared cooldown")
	}
	// Leave no due fixtures for subsequent integration tests.
	exec("UPDATE browseai.github_monitors SET expires_at=clock_timestamp()-interval '1 second' WHERE installation_id IN (99410,99411)")
}
