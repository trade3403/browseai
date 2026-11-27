package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"testing"
)

func TestPostgresTaskControlIntegration(t *testing.T) {
	ownerDSN, authDSN, appDSN := os.Getenv("BROWSEAI_TEST_OWNER_DSN"), os.Getenv("BROWSEAI_TEST_AUTH_DSN"), os.Getenv("BROWSEAI_TEST_APP_DSN")
	if ownerDSN == "" && authDSN == "" && appDSN == "" {
		t.Skip("isolated PostgreSQL integration only")
	}
	if ownerDSN == "" || authDSN == "" || appDSN == "" {
		t.Fatal("incomplete integration configuration")
	}
	open := func(dsn string) *sql.DB {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		return db
	}
	owner, authDB, db := open(ownerDSN), open(authDSN), open(appDSN)
	ctx := context.Background()
	if _, err := owner.Exec("UPDATE browseai.users SET enabled=true WHERE login IN ('mock-a','mock-b')"); err != nil {
		t.Fatal(err)
	}
	a, err := newAuthHTTP(testOrigin, sqlCredentialVerifier{authDB}, sessionService{store: sqlSessionStore{authDB}})
	if err != nil {
		t.Fatal(err)
	}
	p := principal{"00000000-0000-4000-8000-000000000031", "00000000-0000-4000-8000-000000000001", roleUser}
	other := principal{"00000000-0000-4000-8000-000000000032", "00000000-0000-4000-8000-000000000002", roleUser}
	issue := func(p principal) *http.Cookie {
		token, err := a.sessions.issueVerified(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Cookie{Name: sessionCookie, Value: token}
	}
	ca, cb := issue(p), issue(other)
	h := projectRoutes(a, authDB, db)
	s := taskStore{db}
	pr, err := (projectStore{db}).create(ctx, p, projectCreate{"Control test", 6543, "owner/control", "feat/control"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.create(ctx, p, taskCreate{ProjectID: pr.ID, Name: "Control task", Stages: []stageCreate{{"S1", 1}}})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/tasks/" + r.ID
	req := func(action, body string, cookie *http.Cookie, want int) taskRecord {
		t.Helper()
		w := authRequest(h, "POST", path+"/"+action, testOrigin, body, "192.0.2.9", cookie)
		if w.Code != want {
			t.Fatal(action, w.Code, w.Body.String())
		}
		var item taskRecord
		if want == 200 && json.Unmarshal(w.Body.Bytes(), &item) != nil {
			t.Fatal("invalid response")
		}
		return item
	}
	req("approve", `{"revision":0}`, ca, 409)
	req("pause", `{"revision":0}`, ca, 200)
	req("resume", `{"revision":1,"Reconciled":true}`, ca, 400)
	req("resume", `{"revision":1}`, cb, 404)
	if item := req("resume", `{"revision":1}`, ca, 200); item.State != taskQueued || item.Revision != 2 {
		t.Fatal("local resume did not restore queue", item)
	}
	lease, err := (leaseStore{db}).acquire(ctx, p, r.ID, 2, verifiedBranch{6543, "feat/control"})
	if err != nil {
		t.Fatal(err)
	}
	ev := taskEvidence{ReadyToRun: true, LeaseFence: lease.Fence}
	if _, err := s.transition(ctx, p, r.ID, 2, actionReady, ev); err != nil {
		t.Fatal(err)
	}
	if _, err := s.transition(ctx, p, r.ID, 3, actionPrepare, ev); err != nil {
		t.Fatal(err)
	}
	ev.ManualSent = true
	if _, err := s.transition(ctx, p, r.ID, 4, actionConfirmSent, ev); !errors.Is(err, errResourceConflict) {
		t.Fatal("missing approval accepted", err)
	}
	req("approve", `{"revision":4}`, cb, 404)
	req("approve", `{"revision":3}`, ca, 409)
	if item := req("approve", `{"revision":4}`, ca, 200); item.State != taskAwaitApproval || item.Revision != 4 {
		t.Fatal("approval unexpectedly dispatched task", item)
	}
	req("approve", `{"revision":4}`, ca, 409)
	// Approval of an old revision is not usable after a change.
	req("pause", `{"revision":4}`, ca, 200)
	req("resume", `{"revision":5}`, ca, 409)
	if _, err := s.transition(ctx, p, r.ID, 5, actionResume, taskEvidence{ManualResume: true, Reconciled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.transition(ctx, p, r.ID, 6, actionConfirmSent, ev); !errors.Is(err, errResourceConflict) {
		t.Fatal("stale approval accepted", err)
	}
	req("approve", `{"revision":6}`, ca, 200)
	ev.ManualSent = false
	if _, err := s.transition(ctx, p, r.ID, 6, actionConfirmSent, ev); !errors.Is(err, errResourceConflict) {
		t.Fatal("approval treated as manual send", err)
	}
	ev.ManualSent = true
	if item, err := s.transition(ctx, p, r.ID, 6, actionConfirmSent, ev); err != nil || item.State != taskDeveloping {
		t.Fatal("trusted synthetic send transition failed", item, err)
	}
	req("stop", `{"revision":7}`, ca, 200)
	req("resume", `{"revision":8}`, ca, 409)
	if err := (leaseStore{db}).release(ctx, p, r.ID, 8, lease.Fence, true); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := owner.QueryRow("SELECT count(*) FROM browseai.task_approvals WHERE task_id=$1", r.ID).Scan(&count); err != nil || count != 2 {
		t.Fatal("approval ledger mismatch", count, err)
	}
}
