package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

const githubFailureConfigDomain = "browseai-failure-observer-v1"

type githubFailureConfig struct {
	Domain       string   `json:"domain"`
	Grant        string   `json:"grant"`
	Tenant       string   `json:"tenant"`
	Task         string   `json:"task"`
	Project      string   `json:"project"`
	Revision     int64    `json:"revision"`
	Installation int64    `json:"installation"`
	Repository   int64    `json:"repository"`
	FullName     string   `json:"full_name"`
	Branch       string   `json:"branch"`
	HeadSHA      string   `json:"head_sha"`
	Workflow     int64    `json:"workflow"`
	Name         string   `json:"name"`
	Path         string   `json:"path"`
	Required     []string `json:"required"`
	Source       string   `json:"source_digest"`
	PR           int64    `json:"pr"`
	BaseBranch   string   `json:"base_branch"`
	BaseSHA      string   `json:"base_sha"`
	AppIssuer    string   `json:"app_issuer"`
	Expires      string   `json:"expires"`
}

func (c githubFailureConfig) policy() (githubIndependentPolicy, time.Time, error) {
	p := githubIndependentPolicy{
		task:         githubEvidenceTask{c.Project, c.Task, c.Revision},
		execution:    githubEvidencePolicy{githubBinding{c.Tenant, c.Installation, c.Repository, c.FullName}, c.Branch, c.HeadSHA, c.Workflow, c.Name, c.Path, append([]string(nil), c.Required...)},
		sourceDigest: c.Source,
	}
	expires, err := time.Parse(time.RFC3339, c.Expires)
	if err != nil || expires.UTC().Format(time.RFC3339) != c.Expires ||
		c.Domain != githubFailureConfigDomain || !resourceUUID.MatchString(c.Grant) ||
		!p.task.valid() || !p.execution.valid() || !githubWorkflowDigestValid(c.Source) || !githubAppIssuer.MatchString(c.AppIssuer) {
		return githubIndependentPolicy{}, time.Time{}, errGitHubAuthorization
	}
	if c.PR == 0 {
		if c.BaseBranch != "" || c.BaseSHA != "" {
			return githubIndependentPolicy{}, time.Time{}, errGitHubAuthorization
		}
	} else {
		pr := githubPRPolicy{p.execution, c.PR, c.BaseBranch, c.BaseSHA}
		if !pr.valid() {
			return githubIndependentPolicy{}, time.Time{}, errGitHubAuthorization
		}
		p.pr = &pr
	}
	return p, expires, nil
}

func decodeGitHubFailureConfig(raw []byte) (githubFailureConfig, error) {
	var c githubFailureConfig
	if len(raw) == 0 || len(raw) > 65536 || json.Unmarshal(raw, &c) != nil {
		return c, errGitHubAuthorization
	}
	canonical, err := json.Marshal(c)
	if err != nil || !bytes.Equal(canonical, raw) {
		return c, errGitHubAuthorization
	}
	_, _, err = c.policy()
	return c, err
}

func githubFailureGrantApproval(raw []byte, action string, input io.Reader) error {
	if input == nil || (action != "issue" && action != "revoke") {
		return errGitHubAuthorization
	}
	sum := sha256.Sum256(raw)
	expected := "APPROVE " + action + " " + hex.EncodeToString(sum[:]) + "\n"
	line, err := io.ReadAll(io.LimitReader(input, 256))
	if err != nil || string(line) != expected {
		return errGitHubAuthorization
	}
	return nil
}

func githubFailureGrantReady(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errGitHubAuthorization
	}
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT current_user='browseai_failure_grant_operator' AND session_user=current_user
AND NOT r.rolsuper AND NOT r.rolbypassrls AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
AND has_function_privilege(current_user,'browseai.issue_github_failure_grant(uuid,uuid,uuid,uuid,bigint,bigint,bigint,text,text,bigint,text,text,timestamptz)','EXECUTE')
AND has_function_privilege(current_user,'browseai.revoke_github_failure_grant(uuid,uuid)','EXECUTE')
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
WHERE n.nspname='browseai' AND p.proname NOT IN ('issue_github_failure_grant','revoke_github_failure_grant')
AND has_function_privilege(current_user,p.oid,'EXECUTE'))
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='browseai' AND c.relkind IN ('r','p','v','m','f')
AND (has_table_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
OR has_any_column_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,REFERENCES')))
FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`).Scan(&ok)
	if err != nil || !ok {
		return errGitHubAuthorization
	}
	return nil
}

func changeGitHubFailureGrant(ctx context.Context, db *sql.DB, c githubFailureConfig, action string) error {
	p, expires, err := c.policy()
	if err != nil || githubFailureGrantReady(ctx, db) != nil {
		return errGitHubAuthorization
	}
	var ok bool
	switch action {
	case "issue":
		event := "push"
		if p.pr != nil {
			event = "pull_request"
		}
		err = db.QueryRowContext(ctx, "SELECT browseai.issue_github_failure_grant($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)", c.Grant, c.Tenant, c.Task, c.Project, c.Revision, c.Installation, c.Repository, c.Branch, c.HeadSHA, c.Workflow, c.Source, event, expires).Scan(&ok)
	case "revoke":
		err = db.QueryRowContext(ctx, "SELECT browseai.revoke_github_failure_grant($1,$2)", c.Grant, c.Tenant).Scan(&ok)
	default:
		return errGitHubAuthorization
	}
	if err != nil || !ok {
		return errGitHubAuthorization
	}
	return nil
}

// One-shot trusted operator commands; no listener or automated repair action.
func githubFailureCommand(args []string, input io.Reader) error {
	if runtime.GOOS != "linux" || os.Geteuid() <= 0 || len(args) != 2 {
		return errGitHubAuthorization
	}
	action, dir := args[0], args[1]
	raw, err := githubReadNodePreviewFile(dir, "failure.json", 65536)
	if err != nil {
		return err
	}
	c, err := decodeGitHubFailureConfig(raw)
	if err != nil {
		return err
	}
	role, env := "browseai_failure_observer", "BROWSEAI_FAILURE_OBSERVER_DSN"
	switch action {
	case "observe":
	case "issue", "revoke":
		role, env = "browseai_failure_grant_operator", "BROWSEAI_FAILURE_GRANT_DSN"
		if err := githubFailureGrantApproval(raw, action, input); err != nil {
			return err
		}
	default:
		return errGitHubAuthorization
	}
	dsn := os.Getenv(env)
	if !githubRoleDSNValid(dsn, role) {
		return errGitHubAuthorization
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return errGitHubAuthorization
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	if action != "observe" {
		return changeGitHubFailureGrant(ctx, db, c, action)
	}
	p, expires, err := c.policy()
	if err != nil || !time.Now().Before(expires) {
		return errGitHubAuthorization
	}
	f, err := githubOpenNodePreviewFile(dir, "app.pem", 65536)
	if err != nil {
		return err
	}
	f.Close()
	key, err := loadGitHubAppKey(filepath.Join(dir, "app.pem"))
	if err != nil {
		return err
	}
	transport := &http.Transport{TLSHandshakeTimeout: 10 * time.Second, MaxResponseHeaderBytes: 16384}
	defer transport.CloseIdleConnections()
	app, err := newGitHubApp(c.AppIssuer, key, transport)
	if err != nil {
		return err
	}
	_, err = observeAndRecordGitHubFailure(ctx, db, app, p, c.Grant, time.Now)
	return err
}
