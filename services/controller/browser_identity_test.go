package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBrowserIdentityPersistence(t *testing.T) {
	if os.Getenv("BROWSEAI_TEST_OWNER_DSN") == "" {
		t.Skip("requires PostgreSQL integration fixture")
	}
	open := func(key string) *sql.DB {
		t.Helper()
		if os.Getenv(key) == "" {
			t.Fatal("missing database role", key)
		}
		db, err := sql.Open("postgres", os.Getenv(key))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	operator := open("BROWSEAI_TEST_BROWSER_OPERATOR_DSN")
	node := open("BROWSEAI_TEST_BROWSER_NODE_DSN")
	owner := open("BROWSEAI_TEST_OWNER_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if browserNodeReady(ctx, node) != nil || browserOperatorReady(ctx, operator) != nil {
		t.Fatal("dedicated roles rejected")
	}
	if browserNodeReady(ctx, operator) == nil || browserOperatorReady(ctx, node) == nil ||
		browserNodeReady(ctx, owner) == nil || browserOperatorReady(ctx, owner) == nil ||
		browserNodeReady(ctx, nil) == nil || browserOperatorReady(ctx, nil) == nil {
		t.Fatal("incorrect database identity accepted")
	}
	const id = "00000000-0000-4000-8000-000000008001"
	const op1 = "00000000-0000-4000-8000-000000008002"
	const op2 = "00000000-0000-4000-8000-000000008003"
	const op3 = "00000000-0000-4000-8000-000000008004"
	first := sha256.Sum256([]byte("synthetic node credential one"))
	second := sha256.Sum256([]byte("synthetic node credential two"))
	manage := func(generation int64, digest []byte, op, action string, want bool) {
		t.Helper()
		var ok bool
		// A typed nil []byte can be encoded as an empty bytea by lib/pq.
		// Revocation explicitly requires SQL NULL, not an empty credential.
		var credential any
		if digest != nil {
			credential = digest
		}
		err := operator.QueryRowContext(ctx, "SELECT browseai.manage_browser_identity($1,$2,$3,$4,$5)", id, generation, credential, op, action).Scan(&ok)
		if err != nil || ok != want {
			t.Fatal("identity operation", action, ok, err)
		}
	}
	heartbeat := func(generation, sequence int64, digest []byte, want bool) {
		t.Helper()
		var ok bool
		err := node.QueryRowContext(ctx, "SELECT browseai.record_browser_heartbeat($1,$2,$3,$4,4,1)", id, generation, digest, sequence).Scan(&ok)
		if err != nil || ok != want {
			t.Fatal("heartbeat", ok, err)
		}
	}
	manage(1, first[:], op1, "register", true)
	heartbeat(1, 1, first[:], true)
	heartbeat(1, 1, first[:], false)
	heartbeat(1, 2, second[:], false)
	manage(2, second[:], op1, "rotate", false)
	heartbeat(1, 2, first[:], true)
	manage(2, second[:], op2, "rotate", true)
	heartbeat(1, 3, first[:], false)
	heartbeat(2, 1, first[:], false)
	heartbeat(2, 1, second[:], true)
	manage(2, []byte{}, op3, "revoke", false)
	heartbeat(2, 2, second[:], true)
	manage(2, nil, op3, "revoke", true)
	heartbeat(2, 3, second[:], false)
	var count int
	if err := node.QueryRowContext(ctx, "SELECT count(*) FROM browseai.browser_nodes").Scan(&count); err == nil {
		t.Fatal("node can read identity table")
	}
	if err := owner.QueryRowContext(ctx, "SELECT count(*) FROM browseai.browser_node_audit WHERE node_id=$1", id).Scan(&count); err != nil || count != 3 {
		t.Fatal("identity audit mismatch", count, err)
	}
}

func TestBrowserIdentityCommandPersistence(t *testing.T) {
	if os.Getenv("BROWSEAI_TEST_OWNER_DSN") == "" {
		t.Skip("requires PostgreSQL integration fixture")
	}
	operator, err := sql.Open("postgres", os.Getenv("BROWSEAI_TEST_BROWSER_OPERATOR_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer operator.Close()
	node, err := sql.Open("postgres", os.Getenv("BROWSEAI_TEST_BROWSER_NODE_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	const id = "00000000-0000-4000-8000-000000008011"
	const op1 = "00000000-0000-4000-8000-000000008012"
	const op2 = "00000000-0000-4000-8000-000000008013"
	const op3 = "00000000-0000-4000-8000-000000008014"
	dir := t.TempDir()
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")
	command := func(action, generation, operation, destination string) error {
		args := []string{action, id, generation, operation, destination}
		return browserIdentityCommand(ctx, operator, args, strings.NewReader("APPROVE "+strings.Join(args[:4], " ")))
	}
	if err := command("register", "1", op1, first); err != nil {
		t.Fatal("registration failed", err)
	}
	raw, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	if err := persistBrowserHeartbeat(ctx, node, id, string(raw), 1, 1, 4, 0); err != nil {
		t.Fatal("generated credential rejected", err)
	}
	if err := persistBrowserHeartbeat(ctx, node, id, string(raw), 1, 1, 4, 0); err == nil {
		t.Fatal("replayed heartbeat accepted")
	}
	if err := command("rotate", "2", op2, first); err == nil {
		t.Fatal("credential file overwritten")
	}
	if err := command("rotate", "2", op2, second); err != nil {
		t.Fatal("rotation failed", err)
	}
	next, err := os.ReadFile(second)
	if err != nil || string(next) == string(raw) {
		t.Fatal("rotation credential invalid", err)
	}
	if persistBrowserHeartbeat(ctx, node, id, string(raw), 1, 2, 4, 0) == nil ||
		persistBrowserHeartbeat(ctx, node, id, string(next), 2, 1, 4, 0) != nil {
		t.Fatal("rotation not enforced")
	}
	if err := command("revoke", "2", op3, "-"); err != nil {
		t.Fatal("revocation failed", err)
	}
	if persistBrowserHeartbeat(ctx, node, id, string(next), 2, 2, 4, 0) == nil {
		t.Fatal("revoked credential accepted")
	}
}
