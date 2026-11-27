package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

type githubMonitorPolicy struct {
	TenantID       string   `json:"tenant_id"`
	InstallationID int64    `json:"installation_id"`
	RepositoryID   int64    `json:"repository_id"`
	WorkflowID     int64    `json:"workflow_id"`
	Event          string   `json:"event"`
	RequiredJobs   []string `json:"required_jobs"`
}

type githubMonitorPolicyKey struct {
	tenant       string
	installation int64
	repository   int64
	workflow     int64
	event        string
}

func parseGitHubMonitorPolicies(raw []byte) (map[githubMonitorPolicyKey][]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var policies []githubMonitorPolicy
	if dec.Decode(&policies) != nil || len(policies) == 0 || len(policies) > 100 {
		return nil, errGitHubAuthorization
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return nil, errGitHubAuthorization
	}
	result := make(map[githubMonitorPolicyKey][]string)
	for _, p := range policies {
		if !resourceUUID.MatchString(p.TenantID) || p.InstallationID <= 0 || p.RepositoryID <= 0 || p.WorkflowID <= 0 ||
			(p.Event != "push" && p.Event != "pull_request") || len(p.RequiredJobs) == 0 || len(p.RequiredJobs) > 100 {
			return nil, errGitHubAuthorization
		}
		k := githubMonitorPolicyKey{p.TenantID, p.InstallationID, p.RepositoryID, p.WorkflowID, p.Event}
		if _, exists := result[k]; exists {
			return nil, errGitHubAuthorization
		}
		seen := make(map[string]bool)
		for _, name := range p.RequiredJobs {
			if len(name) == 0 || len(name) > 255 || strings.TrimSpace(name) != name || strings.ContainsAny(name, "\r\n\t\x00") || seen[name] {
				return nil, errGitHubAuthorization
			}
			seen[name] = true
		}
		result[k] = append([]string(nil), p.RequiredJobs...)
	}
	return result, nil
}

func githubMonitorPolicyReader(policies map[githubMonitorPolicyKey][]string, observe func(context.Context, githubBinding, string, string, int64, string, []string, time.Time) (githubCIState, error)) githubMonitorReader {
	return func(ctx context.Context, m githubMonitorClaim, now time.Time) (githubCIState, error) {
		k := githubMonitorPolicyKey{m.binding.tenantID, m.binding.installationID, m.binding.repositoryID, m.workflowID, m.event}
		jobs, ok := policies[k]
		if !ok || observe == nil {
			return githubCIUnknown, errGitHubAuthorization
		}
		return observe(ctx, m.binding, m.branch, m.sha, m.workflowID, m.event, append([]string(nil), jobs...), now)
	}
}

func githubMonitorReady(ctx context.Context, db *sql.DB) error {
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT current_user='browseai_monitor'
AND session_user=current_user AND NOT r.rolsuper AND NOT r.rolbypassrls
AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
AND has_function_privilege(current_user,'browseai.claim_github_monitor()','EXECUTE')
AND has_function_privilege(current_user,'browseai.github_monitor_repository(uuid,uuid)','EXECUTE')
AND has_function_privilege(current_user,'browseai.publish_github_monitor(uuid,uuid,text,integer,timestamptz)','EXECUTE')
AND NOT has_function_privilege(current_user,'browseai.claim_github_monitor_v1()','EXECUTE')
AND NOT has_function_privilege(current_user,'browseai.publish_github_monitor_v1(uuid,uuid,text,integer,timestamptz)','EXECUTE')
AND NOT has_function_privilege(current_user,'browseai.record_github_delivery(uuid,bytea,bigint,bigint,text)','EXECUTE')
AND NOT has_function_privilege(current_user,'browseai.provision_github_binding(uuid,bigint,bigint,text,bigint,bigint)','EXECUTE')
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='browseai' AND c.relkind IN ('r','p','v','m','f')
AND has_table_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER'))
FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`).Scan(&ok)
	if err != nil || !ok {
		return errGitHubAuthorization
	}
	return nil
}

// Single worker per process. Database fencing coordinates other processes.
// Never hold a transaction during the idle wait or remote request.
func runGitHubMonitorService(ctx context.Context, step func(context.Context) (bool, error), wait githubPollWait) error {
	if step == nil || wait == nil {
		return errGitHubAuthorization
	}
	failures := 0
	for ctx.Err() == nil {
		found, err := step(ctx)
		if ctx.Err() != nil {
			break
		}
		delay := time.Second
		if err != nil {
			failures++
			if failures >= 8 {
				return errGitHubUnavailable
			}
			delay = time.Duration(1<<failures) * time.Second
			if delay > time.Minute {
				delay = time.Minute
			}
		} else {
			failures = 0
			if !found {
				delay = 5 * time.Second
			}
		}
		if err := wait(ctx, delay); err != nil {
			if ctx.Err() != nil {
				break
			}
			return err
		}
	}
	return ctx.Err()
}

func githubMonitorCommand() error {
	dsn := os.Getenv("BROWSEAI_MONITOR_DSN")
	if !githubRoleDSNValid(dsn, "browseai_monitor") {
		return errGitHubAuthorization
	}
	raw, err := readProtectedGitHubFile(os.Getenv("BROWSEAI_MONITOR_POLICY_FILE"), 256*1024)
	if err != nil {
		return errGitHubAuthorization
	}
	policies, err := parseGitHubMonitorPolicies(raw)
	if err != nil {
		return err
	}
	key, err := loadGitHubAppKey(os.Getenv("BROWSEAI_GITHUB_KEY_FILE"))
	if err != nil {
		return err
	}
	app, err := newGitHubApp(os.Getenv("BROWSEAI_GITHUB_APP_ISSUER"), key, nil)
	if err != nil {
		return err
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return errGitHubUnavailable
	}
	defer db.Close()
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	check, cancel := context.WithTimeout(ctx, 3*time.Second)
	err = githubMonitorReady(check, db)
	cancel()
	if err != nil {
		return err
	}
	store := githubMonitorStore{db}
	read := githubMonitorPolicyReader(policies, app.pollActions)
	err = runGitHubMonitorService(ctx, func(ctx context.Context) (bool, error) {
		return store.step(ctx, read)
	}, waitGitHubPoll)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return nil
	}
	return err
}
