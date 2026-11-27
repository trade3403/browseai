package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestPostgresGitHubDelivery(t *testing.T) {
	ownerDSN := os.Getenv("BROWSEAI_TEST_OWNER_DSN")
	webhookDSN := os.Getenv("BROWSEAI_TEST_WEBHOOK_DSN")
	operatorDSN := os.Getenv("BROWSEAI_TEST_GITHUB_OPERATOR_DSN")
	appDSN := os.Getenv("BROWSEAI_TEST_APP_DSN")
	if ownerDSN == "" && webhookDSN == "" {
		t.Skip("isolated PostgreSQL integration only")
	}
	if ownerDSN == "" || webhookDSN == "" || operatorDSN == "" || appDSN == "" {
		t.Fatal("incomplete test configuration")
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
	owner, operator, receiver, app := open(ownerDSN), open(operatorDSN), open(webhookDSN), open(appDSN)
	ctx := context.Background()
	if err := githubWebhookReady(ctx, receiver); err != nil {
		t.Fatal("receiver role rejected", err)
	}
	if err := githubWebhookReady(ctx, app); err == nil {
		t.Fatal("runtime accepted as receiver")
	}
	tenant := "00000000-0000-4000-8000-000000000001"
	var ok bool
	if err := operator.QueryRow("SELECT browseai.provision_github_binding($1,9123,9456,'owner/delivery',12,34)", tenant).Scan(&ok); err != nil || !ok {
		t.Fatal("fixture binding rejected", err)
	}
	d := githubWebhookDelivery{
		deliveryID:     "12345678-1234-1234-1234-123456789abc",
		event:          "workflow_run",
		installationID: 9123,
		repositoryID:   9456,
		bodyDigest:     sha256.Sum256([]byte("synthetic body")),
	}
	store := githubDeliveryStore{receiver}
	check := func(d githubWebhookDelivery, want string) {
		t.Helper()
		got, err := store.record(ctx, d)
		if err != nil || got != want {
			t.Fatal("unexpected delivery result", got, err)
		}
	}
	key := []byte(strings.Repeat("s", 32))
	body := `{"installation":{"id":9123},"repository":{"id":9456}}`
	d.bodyDigest = sha256.Sum256([]byte(body))
	handler := newGitHubWebhookHandler(key, store)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, signedWebhookRequest(key, body))
	if w.Code != 202 {
		t.Fatal("HTTP delivery rejected", w.Code)
	}
	check(d, "duplicate")
	check(d, "duplicate")
	replay := d
	replay.deliveryID = "12345678-1234-1234-1234-123456789abd"
	replay.event = "push"
	check(replay, "duplicate")
	conflict := d
	conflict.bodyDigest = sha256.Sum256([]byte("different body"))
	check(conflict, "rejected")
	wrong := replay
	wrong.repositoryID = 9457
	check(wrong, "rejected")
	wrong = replay
	wrong.installationID = 9124
	check(wrong, "rejected")
	if _, err := (githubDeliveryStore{app}).record(ctx, d); err == nil {
		t.Fatal("runtime can record deliveries")
	}
	if _, err := receiver.Exec("SELECT * FROM browseai.github_deliveries"); err == nil {
		t.Fatal("receiver can read ledger")
	}
	var n int
	if err := owner.QueryRow("SELECT count(*) FROM browseai.github_deliveries WHERE tenant_id=$1 AND installation_id=9123 AND repository_id=9456", tenant).Scan(&n); err != nil || n != 1 {
		t.Fatal("unexpected ledger rows", n, err)
	}
	if _, err := owner.Exec("UPDATE browseai.github_installations SET enabled=false WHERE installation_id=9123"); err != nil {
		t.Fatal(err)
	}
	check(d, "rejected")
}
