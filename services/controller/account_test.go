package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestAccountCommandValidation(t *testing.T) {
	if !adminDSNValid("postgres://browseai_admin@db.example/db?sslmode=verify-full") ||
		adminDSNValid("postgres://postgres@db.example/db?sslmode=verify-full") ||
		adminDSNValid("postgres://browseai_admin@db.example/db?sslmode=disable") {
		t.Fatal("operator DSN validation failed")
	}
	for _, args := range [][]string{nil, {"provision", "bad login"}, {"provision", "valid"}, {"disable", "valid", "extra"}, {"unknown", "valid"}} {
		if err := accountCommand(context.Background(), nil, args, strings.NewReader(""), &bytes.Buffer{}); err == nil {
			t.Fatal("invalid command accepted", args)
		}
	}
}

func TestPostgresAccountLifecycle(t *testing.T) {
	ownerDSN, authDSN, adminDSN := os.Getenv("BROWSEAI_TEST_OWNER_DSN"), os.Getenv("BROWSEAI_TEST_AUTH_DSN"), os.Getenv("BROWSEAI_TEST_ADMIN_DSN")
	if ownerDSN == "" && authDSN == "" && adminDSN == "" {
		t.Skip("isolated PostgreSQL integration only")
	}
	if ownerDSN == "" || authDSN == "" || adminDSN == "" {
		t.Fatal("incomplete test configuration")
	}
	open := func(dsn string) *sql.DB {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	owner, auth, admin := open(ownerDSN), open(authDSN), open(adminDSN)
	ctx := context.Background()
	const login = "lifecycle-test"
	const password = "synthetic-first-password"
	const replacement = "synthetic-new-password"
	var output bytes.Buffer
	run := func(args []string, input string) {
		t.Helper()
		output.Reset()
		if err := accountCommand(ctx, admin, args, strings.NewReader(input), &output); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(output.String(), "password") {
			t.Fatal("unexpected secret output")
		}
	}
	run([]string{"provision", login, "Lifecycle workspace"}, password+"\n")
	if err := accountCommand(ctx, admin, []string{"provision", login, "Duplicate workspace"}, strings.NewReader(password), &output); err == nil {
		t.Fatal("duplicate login allowed")
	}
	v := sqlCredentialVerifier{auth}
	s := sessionService{store: sqlSessionStore{auth}}
	first, err := v.verifyAndIssue(ctx, login, password)
	if err != nil {
		t.Fatal(err)
	}
	p, oldHash, err := v.verifiedCandidate(ctx, login, password)
	if err != nil || p.role != roleUser {
		t.Fatal("provisioned identity invalid", err)
	}
	run([]string{"disable", login}, "")
	run([]string{"enable", login}, "")
	if _, err := s.authenticate(ctx, first); !errors.Is(err, errUnauthenticated) {
		t.Fatal("disabled session resurrected", err)
	}
	second, err := v.verifyAndIssue(ctx, login, password)
	if err != nil {
		t.Fatal(err)
	}
	run([]string{"reset-password", login}, replacement+"\n")
	if _, err := s.authenticate(ctx, second); !errors.Is(err, errUnauthenticated) {
		t.Fatal("reset session still accepted", err)
	}
	if _, err := v.verifyAndIssue(ctx, login, password); !errors.Is(err, errUnauthenticated) {
		t.Fatal("old password accepted", err)
	}
	var issued bool
	if err := auth.QueryRow("SELECT browseai.issue_verified_session($1,$2,$3,$4)", p.userID, p.tenantID, make([]byte, 32), oldHash).Scan(&issued); err != nil || issued {
		t.Fatal("stale password proof accepted", err)
	}
	third, err := v.verifyAndIssue(ctx, login, replacement)
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		if _, err := owner.Exec("UPDATE browseai.tenants SET enabled=$1 WHERE id=$2", enabled, p.tenantID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.authenticate(ctx, third); !errors.Is(err, errUnauthenticated) {
		t.Fatal("tenant session resurrected", err)
	}
	var count int
	if err := owner.QueryRow("SELECT count(*) FROM browseai.account_audit WHERE user_id=$1 AND operator_role='browseai_admin'", p.userID).Scan(&count); err != nil || count != 4 {
		t.Fatal("operator audit mismatch", count, err)
	}
	if err := owner.QueryRow("SELECT count(*) FROM browseai.tenants WHERE name='Duplicate workspace'").Scan(&count); err != nil || count != 0 {
		t.Fatal("duplicate provision leaked tenant", count, err)
	}
	if _, err := admin.Exec("SELECT password_hash FROM browseai.users"); err == nil {
		t.Fatal("admin role can directly read hashes")
	}
	if _, err := auth.Exec("SELECT browseai.set_account_enabled($1,false)", login); err == nil {
		t.Fatal("authentication role can administer accounts")
	}
}
