package main

import (
	"bytes"
	"context"
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

const githubCIAcceptanceConfigDomain = "browseai-ci-acceptance-v1"

// Reuse the independently provisioned task/workflow/PR policy shape. The
// nested Grant is the approval ID, not a failure authorization.
type githubCIAcceptanceConfig struct {
	Domain     string              `json:"domain"`
	Policy     githubFailureConfig `json:"policy"`
	Actor      string              `json:"actor"`
	Push       string              `json:"push"`
	PushDigest string              `json:"push_digest"`
	PR         string              `json:"pr"`
	PRDigest   string              `json:"pr_digest"`
	Verifier   string              `json:"verifier"`
	Key        string              `json:"key"`
}

func (c githubCIAcceptanceConfig) parameters() (githubIndependentPolicy, githubCIAcceptanceApproval, error) {
	p, _, err := c.Policy.policy()
	a := githubCIAcceptanceApproval{}
	if err != nil || p.pr == nil || c.Domain != githubCIAcceptanceConfigDomain ||
		!resourceUUID.MatchString(c.Actor) || !resourceUUID.MatchString(c.Push) || !resourceUUID.MatchString(c.PR) || c.Push == c.PR ||
		!resourceUUID.MatchString(c.Verifier) || !resourceUUID.MatchString(c.Key) ||
		!githubWorkflowDigestValid(c.PushDigest) || !githubWorkflowDigestValid(c.PRDigest) || c.PushDigest == c.PRDigest {
		return p, a, errGitHubAuthorization
	}
	p.verifierID, p.keyID = c.Verifier, c.Key
	a.actorID, a.approvalID, a.tenantID, a.task = c.Actor, c.Policy.Grant, c.Policy.Tenant, p.task
	a.pushID, a.prID = c.Push, c.PR
	if _, err := hex.Decode(a.pushDigest[:], []byte(c.PushDigest)); err != nil {
		return p, a, errGitHubAuthorization
	}
	if _, err := hex.Decode(a.prDigest[:], []byte(c.PRDigest)); err != nil {
		return p, a, errGitHubAuthorization
	}
	return p, a, nil
}

func decodeGitHubCIAcceptanceConfig(raw []byte) (githubCIAcceptanceConfig, error) {
	var c githubCIAcceptanceConfig
	if len(raw) == 0 || len(raw) > 65536 || json.Unmarshal(raw, &c) != nil {
		return c, errGitHubAuthorization
	}
	canonical, err := json.Marshal(c)
	if err != nil || !bytes.Equal(raw, canonical) {
		return c, errGitHubAuthorization
	}
	_, _, err = c.parameters()
	return c, err
}

func loadGitHubCIAcceptanceEvidence(ctx context.Context, db *sql.DB, a githubCIAcceptanceApproval, clock func() time.Time) (githubVerifiedTaskEvidence, githubVerifiedTaskEvidence, error) {
	var push, pr githubVerifiedTaskEvidence
	fail := func() (githubVerifiedTaskEvidence, githubVerifiedTaskEvidence, error) {
		return githubVerifiedTaskEvidence{}, githubVerifiedTaskEvidence{}, errGitHubAuthorization
	}
	if db == nil || clock == nil {
		return fail()
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, err := db.QueryContext(ctx, "SELECT id::text,event_type,statement,signature,evidence_digest,observed_at,issued_at,expires_at FROM browseai.read_github_acceptance_evidence($1,$2,$3,$4,$5)", a.approvalID, a.tenantID, a.task.taskID, a.task.revision, a.actorID)
	if err != nil {
		return fail()
	}
	defer rows.Close()
	seen := make(map[string]bool)
	for rows.Next() {
		var id, event string
		var v githubVerifiedTaskEvidence
		var digest []byte
		if err := rows.Scan(&id, &event, &v.statement, &v.signature, &digest, &v.observed, &v.issued, &v.expires); err != nil || len(digest) != 32 || seen[event] {
			return fail()
		}
		copy(v.digest[:], digest)
		if !githubEvidenceEnvelopeValid(v, clock()) {
			return fail()
		}
		switch {
		case event == "push" && id == a.pushID && v.digest == a.pushDigest:
			push = v
		case event == "pull_request" && id == a.prID && v.digest == a.prDigest:
			pr = v
		default:
			return fail()
		}
		seen[event] = true
	}
	if rows.Err() != nil || len(seen) != 2 || ctx.Err() != nil {
		return fail()
	}
	return push, pr, nil
}

func runGitHubCIAuthorizedAcceptance(ctx context.Context, db *sql.DB, verifier githubVerifierStore, pushApp, prApp *githubApp, p githubIndependentPolicy, a githubCIAcceptanceApproval, clock func() time.Time) error {
	if clock == nil || githubCIAcceptanceRoleReady(ctx, db, "browseai_ci_acceptor") != nil {
		return errGitHubAuthorization
	}
	push, pr, err := loadGitHubCIAcceptanceEvidence(ctx, db, a, clock)
	if err != nil {
		return err
	}
	plan, err := verifier.prepareCIAcceptance(ctx, pushApp, prApp, p, a, push, pr, clock)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return errGitHubAuthorization
	}
	defer tx.Rollback()
	if err := plan.applyAuthorized(ctx, tx, clock); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return errGitHubAuthorization
	}
	return nil
}

func githubCIAcceptanceCommand(args []string, input io.Reader) error {
	if runtime.GOOS != "linux" || os.Geteuid() <= 0 || len(args) != 2 {
		return errGitHubAuthorization
	}
	action, dir := args[0], args[1]
	raw, err := githubReadNodePreviewFile(dir, "acceptance.json", 65536)
	if err != nil {
		return err
	}
	c, err := decodeGitHubCIAcceptanceConfig(raw)
	if err != nil {
		return err
	}
	p, a, err := c.parameters()
	if err != nil {
		return err
	}
	role, env := "browseai_ci_acceptor", "BROWSEAI_CI_ACCEPTOR_DSN"
	switch action {
	case "accept":
	case "issue", "revoke":
		role, env = "browseai_ci_approval_operator", "BROWSEAI_CI_APPROVAL_DSN"
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
	if githubCIAcceptanceRoleReady(ctx, db, role) != nil {
		return errGitHubAuthorization
	}
	if action != "accept" {
		return changeGitHubCIAcceptanceGrant(ctx, db, p, a, action)
	}
	_, expires, err := c.Policy.policy()
	if err != nil || !time.Now().Before(expires) {
		return errGitHubAuthorization
	}
	readerDSN := os.Getenv("BROWSEAI_CI_VERIFIER_READER_DSN")
	if !githubRoleDSNValid(readerDSN, "browseai_verifier_reader") {
		return errGitHubAuthorization
	}
	reader, err := sql.Open("postgres", readerDSN)
	if err != nil {
		return errGitHubAuthorization
	}
	defer reader.Close()
	reader.SetMaxOpenConns(1)
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
	app, err := newGitHubApp(c.Policy.AppIssuer, key, transport)
	if err != nil {
		return err
	}
	return runGitHubCIAuthorizedAcceptance(ctx, db, githubVerifierStore{reader}, app, app, p, a, time.Now)
}

func changeGitHubCIAcceptanceGrant(ctx context.Context, db *sql.DB, p githubIndependentPolicy, a githubCIAcceptanceApproval, action string) error {
	if db == nil || (action != "issue" && action != "revoke") {
		return errGitHubAuthorization
	}
	query := githubCIGrantSQL
	plan := githubCIAcceptancePlan{approval: a, workflow: p.execution.workflowID, source: p.sourceDigest}
	values := plan.authorizedArgs()
	if action == "revoke" {
		query = "SELECT browseai.revoke_github_ci_acceptance_grant($1,$2)"
		values = []any{a.approvalID, a.tenantID}
	}
	var ok bool
	if err := db.QueryRowContext(ctx, query, values...).Scan(&ok); err != nil || !ok {
		return errGitHubAuthorization
	}
	return nil
}
