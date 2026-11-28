package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
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
