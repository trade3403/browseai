package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestPostgresMutationIntegration(t *testing.T) {
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
	owner, authDB, appDB := open(ownerDSN), open(authDSN), open(appDSN)
	ctx := context.Background()
	if _, err := owner.Exec("UPDATE browseai.users SET enabled=true WHERE login IN ('mock-a','mock-b')"); err != nil {
		t.Fatal(err)
	}
	a, err := newAuthHTTP(testOrigin, sqlCredentialVerifier{authDB}, sessionService{store: sqlSessionStore{authDB}})
	if err != nil {
		t.Fatal(err)
	}
	p1 := principal{"00000000-0000-4000-8000-000000000031", "00000000-0000-4000-8000-000000000001", roleUser}
	p2 := principal{"00000000-0000-4000-8000-000000000032", "00000000-0000-4000-8000-000000000002", roleUser}
	issue := func(p principal) string {
		token, err := a.sessions.issueVerified(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	ca, cb := issue(p1), issue(p2)
	h := projectRoutes(a, authDB, appDB)
	request := func(method, path, body, key, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, testOrigin+path, strings.NewReader(body))
		r.Header.Set("Origin", testOrigin)
		r.Header.Set("Content-Type", "application/json")
		if key != "" {
			r.Header.Set("Idempotency-Key", key)
		}
		r.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	const body = `{"name":"Idempotency test","repository_id":987,"repository":"owner/idempotency","code_branch":"feat/idem"}`
	if w := request("POST", "/api/projects", body, "", ca); w.Code != 400 {
		t.Fatal("missing key accepted", w.Code)
	}
	results := make(chan *httptest.ResponseRecorder, 2)
	for i := 0; i < 2; i++ {
		go func() { results <- request("POST", "/api/projects", body, "create-same", ca) }()
	}
	first, second := <-results, <-results
	var pa, replay projectRecord
	if first.Code != 201 || second.Code != 201 || json.Unmarshal(first.Body.Bytes(), &pa) != nil || json.Unmarshal(second.Body.Bytes(), &replay) != nil || pa.ID != replay.ID || pa.ID == "" {
		t.Fatal("duplicate mutation", first.Code, second.Code)
	}
	if w := request("POST", "/api/projects", strings.Replace(body, "Idempotency test", "Changed", 1), "create-same", ca); w.Code != 409 {
		t.Fatal("key reuse accepted", w.Code)
	}
	if w := request("POST", "/api/projects", body, "create-same", cb); w.Code != 201 {
		t.Fatal("other tenant key collision", w.Code)
	}
	path := "/api/projects/" + pa.ID
	for i := 0; i < 2; i++ {
		w := request("PATCH", path, `{"name":"Updated","revision":0}`, "rename-same", ca)
		var item projectRecord
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &item) != nil || item.Revision != 1 {
			t.Fatal("rename replay failed", w.Code)
		}
	}
	for i := 0; i < 2; i++ {
		w := request("DELETE", path+"?revision=1", "", "delete-same", ca)
		if w.Code != 204 {
			t.Fatal("delete replay failed", w.Code)
		}
	}
	var audits, events int
	if err := owner.QueryRow("SELECT count(*) FROM browseai.audit_logs WHERE resource_id=$1", pa.ID).Scan(&audits); err != nil || audits != 3 {
		t.Fatal("audit count", audits, err)
	}
	if err := owner.QueryRow("SELECT count(*) FROM browseai.events WHERE resource_id=$1", pa.ID).Scan(&events); err != nil || events != 3 {
		t.Fatal("event count", events, err)
	}
	// A failed write must not leave a cached response or an audit/event.
	w := request("PATCH", path, `{"name":"Missing","revision":1}`, "failed-rename", ca)
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	var keys int
	if err := owner.QueryRow("SELECT count(*) FROM browseai.idempotency_keys WHERE tenant_id=$1 AND request_key='failed-rename'", p1.tenantID).Scan(&keys); err != nil || keys != 0 {
		t.Fatal("failed mutation persisted", keys, err)
	}
	_, err = tenantTx(ctx, appDB, p2, func(tx *sql.Tx) (bool, error) {
		if err := tx.QueryRow("SELECT count(*) FROM browseai.audit_logs WHERE resource_id=$1", pa.ID).Scan(&audits); err != nil {
			return false, err
		}
		if err := tx.QueryRow("SELECT count(*) FROM browseai.events WHERE resource_id=$1", pa.ID).Scan(&events); err != nil {
			return false, err
		}
		return true, nil
	})
	if err != nil || audits != 0 || events != 0 {
		t.Fatal("cross tenant audit/event exposure", audits, events, err)
	}
	_, err = tenantTx(ctx, appDB, p1, func(tx *sql.Tx) (bool, error) {
		_, err := tx.Exec("DELETE FROM browseai.audit_logs WHERE resource_id=$1", pa.ID)
		return false, err
	})
	if err == nil {
		t.Fatal("audit delete allowed")
	}
}
