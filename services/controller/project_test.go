package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestProjectInput(t *testing.T) {
	in := projectCreate{"Sample", 123, "owner/demo", "feat/demo"}
	if !in.valid() {
		t.Fatal("valid project denied")
	}
	for _, branch := range []string{"../x", "/x", "x//y", "x.lock", "ai-control/x", "x.", "x y", "-x"} {
		bad := in
		bad.CodeBranch = branch
		if bad.valid() {
			t.Fatal("invalid branch accepted", branch)
		}
	}
	if _, err := tenantTx(context.Background(), nil, principal{}, func(*sql.Tx) (bool, error) { return true, nil }); err != errForbidden {
		t.Fatal("missing principal accepted")
	}
}

func TestPostgresProjectIntegration(t *testing.T) {
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
	if err := projectDatabaseReady(ctx, appDB); err != nil {
		t.Fatal(err)
	}
	hash, err := hashPassword("synthetic-password-only")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.Exec("UPDATE browseai.users SET password_hash=$1,enabled=true WHERE login IN ('mock-a','mock-b')", hash); err != nil {
		t.Fatal(err)
	}
	a, err := newAuthHTTP(testOrigin, sqlCredentialVerifier{authDB}, sessionService{store: sqlSessionStore{authDB}})
	if err != nil {
		t.Fatal(err)
	}
	h := projectRoutes(a, authDB, appDB)
	request := func(method, path, body string, cookie *http.Cookie) *http.Response {
		return authRequest(h, method, path, testOrigin, body, "192.0.2.7", cookie).Result()
	}
	login := func(name string) *http.Cookie {
		w := request("POST", "/api/auth/login", `{"login":"`+name+`","password":"synthetic-password-only"}`, nil)
		defer w.Body.Close()
		if w.StatusCode != 204 {
			t.Fatal("login", w.StatusCode)
		}
		return w.Cookies()[0]
	}
	ca, cb := login("mock-a"), login("mock-b")
	check := func(method, path, body string, cookie *http.Cookie, want int) *http.Response {
		w := request(method, path, body, cookie)
		t.Cleanup(func() { w.Body.Close() })
		if w.StatusCode != want {
			t.Fatalf("%s %s: got %d want %d", method, path, w.StatusCode, want)
		}
		return w
	}
	check("GET", "/readyz", "", nil, 200)
	check("GET", "/api/projects", "", nil, 401)
	create := check("POST", "/api/projects", `{"name":"New project","repository_id":456,"repository":"owner/another","code_branch":"feat/test"}`, ca, 201)
	var item projectRecord
	if err := json.NewDecoder(create.Body).Decode(&item); err != nil || !resourceUUID.MatchString(item.ID) || item.Revision != 0 {
		t.Fatal("invalid created project", err, item)
	}
	path := "/api/projects/" + item.ID
	check("GET", path, "", ca, 200)
	check("GET", path, "", cb, 404)
	check("PATCH", path, `{"name":"stolen","revision":0}`, cb, 404)
	check("DELETE", path+"?revision=0", "", cb, 404)
	check("POST", "/api/projects", `{"name":"bad","tenant_id":"other"}`, ca, 400)
	check("PATCH", path, `{"name":"new"}`, ca, 400)
	check("PATCH", path, `{"name":"renamed","revision":0}`, ca, 200)
	check("PATCH", path, `{"name":"stale","revision":0}`, ca, 409)
	check("DELETE", path+"?revision=0", "", ca, 409)
	check("DELETE", "/api/projects/00000000-0000-4000-8000-000000000011?revision=0", "", ca, 409)
	for _, cookie := range []*http.Cookie{ca, cb} {
		w := check("GET", "/api/projects", "", cookie, 200)
		var result struct {
			Projects []projectRecord `json:"projects"`
		}
		if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		seen := false
		for _, p := range result.Projects {
			seen = seen || p.ID == item.ID
			if cookie == cb && strings.Contains(p.Name, "Project A") {
				t.Fatal("cross tenant listing")
			}
		}
		if seen != (cookie == ca) {
			t.Fatal("tenant list mismatch")
		}
	}
	check("DELETE", path+"?revision=1", "", ca, 204)
	check("GET", path, "", ca, 404)
	// A reused application connection must not retain a tenant after commit.
	appDB.SetMaxOpenConns(1)
	var count int
	if err := appDB.QueryRow("SELECT count(*) FROM browseai.projects").Scan(&count); err != nil || count != 0 {
		t.Fatal("tenant context escaped transaction", count, err)
	}
}
