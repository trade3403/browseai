package main

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"
)

func testGitHubCIAcceptanceGrant(t *testing.T, ctx context.Context, owner *sql.DB, p githubCIAcceptancePlan, clock func() time.Time, verifier githubVerifierStore, pushApp, prApp, badApp *githubApp, policy githubIndependentPolicy) {
	t.Helper()
	open := func(env, role string) *sql.DB {
		t.Helper()
		dsn := os.Getenv(env)
		if dsn == "" {
			t.Fatal("missing acceptance role fixture", env)
		}
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		if err := githubCIAcceptanceRoleReady(ctx, db, role); err != nil {
			t.Fatal("role rejected", role, err)
		}
		return db
	}
	operator := open("BROWSEAI_TEST_CI_APPROVAL_DSN", "browseai_ci_approval_operator")
	acceptor := open("BROWSEAI_TEST_CI_ACCEPTOR_DSN", "browseai_ci_acceptor")
	if err := githubCIAcceptanceRoleReady(ctx, owner, "browseai_ci_acceptor"); err == nil {
		t.Fatal("owner accepted")
	}
	a := p.approval
	if _, err := owner.ExecContext(ctx, "INSERT INTO browseai.users(id,login,password_hash) VALUES($1,'ci-grant-fixture','synthetic')", a.actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ExecContext(ctx, "INSERT INTO browseai.memberships(tenant_id,user_id,role) VALUES($1,$2,'platform_admin')", a.tenantID, a.actorID); err != nil {
		t.Fatal(err)
	}
	check := func(db *sql.DB, query string, want bool, args []any) {
		t.Helper()
		var ok bool
		if err := db.QueryRowContext(ctx, query, args...).Scan(&ok); err != nil || ok != want {
			t.Fatal("unexpected grant result", ok, err)
		}
	}
	args := p.authorizedArgs()
	check(acceptor, githubCIAuthorizedSQL, false, args)
	bad := append([]any(nil), args...)
	bad[8] = make([]byte, 32)
	check(operator, githubCIGrantSQL, false, bad)
	check(operator, githubCIGrantSQL, true, args)
	if _, _, err := loadGitHubCIAcceptanceEvidence(ctx, operator, a, clock); err == nil {
		t.Fatal("operator could read evidence")
	}
	if _, _, err := loadGitHubCIAcceptanceEvidence(ctx, acceptor, a, clock); err != nil {
		t.Fatal("authorized pair not readable", err)
	}
	wrong := a
	wrong.actorID = "00000000-0000-4000-8000-000000009999"
	if _, _, err := loadGitHubCIAcceptanceEvidence(ctx, acceptor, wrong, clock); err == nil {
		t.Fatal("foreign actor could read evidence")
	}
	if err := runGitHubCIAuthorizedAcceptance(ctx, acceptor, verifier, pushApp, badApp, policy, a, clock); err == nil {
		t.Fatal("changed source accepted")
	}
	// Exercise the successful runtime verification path but preserve the
	// fixture for the subsequent rollback and competing-outcome tests.
	push, pr, err := loadGitHubCIAcceptanceEvidence(ctx, acceptor, a, clock)
	if err != nil {
		t.Fatal(err)
	}
	p, err = verifier.prepareCIAcceptance(ctx, pushApp, prApp, policy, a, push, pr, clock)
	if err != nil {
		t.Fatal("authorized preflight failed", err)
	}
	check(operator, githubCIGrantSQL, false, args)
	check(acceptor, githubCIAuthorizedSQL, false, bad)
	// A rollback must undo both evidence marks and grant consumption.
	tx, err := acceptor.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := p.applyAuthorized(ctx, tx, clock); err != nil {
		t.Fatal("authorized acceptance rejected", err)
	}
	if err := p.applyAuthorized(ctx, tx, clock); err == nil {
		t.Fatal("grant replay accepted")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var unused bool
	if err := owner.QueryRowContext(ctx, "SELECT acceptance_id IS NULL FROM browseai.github_ci_acceptance_grants WHERE id=$1", a.approvalID).Scan(&unused); err != nil || !unused {
		t.Fatal("grant not rolled back", unused, err)
	}
	check(operator, "SELECT browseai.revoke_github_ci_acceptance_grant($1,$2)", true, []any{a.approvalID, a.tenantID})
	check(acceptor, githubCIAuthorizedSQL, false, args)
	if _, _, err := loadGitHubCIAcceptanceEvidence(ctx, acceptor, a, clock); err == nil {
		t.Fatal("revoked grant could read evidence")
	}
	var count int
	if err := owner.QueryRowContext(ctx, "SELECT count(*) FROM browseai.github_ci_evidence WHERE id IN($1,$2) AND consumed_at IS NOT NULL", a.pushID, a.prID).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial evidence consumption", count, err)
	}
}
