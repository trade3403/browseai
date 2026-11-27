package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestPasswordHash(t *testing.T) {
	const password = "synthetic-password-only"
	h, err := hashPassword(password)
	if err != nil || !strings.HasPrefix(h, passwordPrefix) || !checkPassword(password, h) {
		t.Fatal("password hash round trip failed", err)
	}
	h2, err := hashPassword(password)
	if err != nil || h == h2 {
		t.Fatal("salt must vary", err)
	}
	for _, bad := range []string{"", "malformed", strings.Replace(h, "m=65536", "m=99999", 1)} {
		if checkPassword(password, bad) {
			t.Fatal("invalid hash accepted")
		}
	}
	if checkPassword("wrong", h) || checkPassword("", h) {
		t.Fatal("wrong password accepted")
	}
	if _, err := hashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
}

func TestAuthenticatedRuntimeBoundary(t *testing.T) {
	valid := "postgres://browseai_auth@db.example/test?sslmode=verify-full"
	if err := validateAuthRuntime("127.0.0.1:8080", valid); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ addr, dsn string }{
		{"0.0.0.0:8080", valid},
		{"127.0.0.1:8080", strings.Replace(valid, "verify-full", "disable", 1)},
		{"127.0.0.1:8080", strings.Replace(valid, "browseai_auth", "postgres", 1)},
	} {
		if validateAuthRuntime(tc.addr, tc.dsn) == nil {
			t.Fatal("unsafe runtime accepted")
		}
	}
	a, _ := testAuth(t, testVerifier{})
	h := authenticatedRoutes(a, func(context.Context) error { return errors.New("offline") })
	for _, tc := range []struct {
		path string
		code int
	}{{"/healthz", 200}, {"/readyz", 503}, {"/api/projects", 404}, {"/api/auth/me", 401}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
		if w.Code != tc.code {
			t.Fatal(tc.path, w.Code)
		}
	}
}

// Runs only against the isolated CI database. Separate owner and auth role
// connections exercise real SQL, not a mock credential or session adapter.
func TestPostgresAuthIntegration(t *testing.T) {
	ownerDSN := os.Getenv("BROWSEAI_TEST_OWNER_DSN")
	authDSN := os.Getenv("BROWSEAI_TEST_AUTH_DSN")
	if ownerDSN == "" && authDSN == "" {
		t.Skip("isolated PostgreSQL integration job only")
	}
	if ownerDSN == "" || authDSN == "" {
		t.Fatal("incomplete integration configuration")
	}
	owner, err := sql.Open("postgres", ownerDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	db, err := sql.Open("postgres", authDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if err := authDatabaseReady(ctx, db); err != nil {
		t.Fatal(err)
	}
	hash, err := hashPassword("synthetic-password-only")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.ExecContext(ctx, "UPDATE browseai.users SET password_hash=$1 WHERE login IN ('mock-a','mock-b')", hash); err != nil {
		t.Fatal(err)
	}
	a, err := newAuthHTTP(testOrigin, sqlCredentialVerifier{db}, sessionService{store: sqlSessionStore{db}})
	if err != nil {
		t.Fatal(err)
	}
	h := authenticatedRoutes(a, func(ctx context.Context) error { return authDatabaseReady(ctx, db) })
	for i, login := range []string{"mock-a", "mock-b"} {
		body := `{"login":"` + login + `","password":"synthetic-password-only"}`
		w := authRequest(h, "POST", "/api/auth/login", testOrigin, body, "192.0.2."+string(rune('1'+i)), nil)
		if w.Code != 204 {
			t.Fatal("login failed", w.Code)
		}
		cookie := w.Result().Cookies()[0]
		me := authRequest(h, "GET", "/api/auth/me", "", "", "192.0.2.9", cookie)
		wantTenant := "00000000-0000-4000-8000-00000000000" + string(rune('1'+i))
		if me.Code != 200 || !strings.Contains(me.Body.String(), wantTenant) {
			t.Fatal("incorrect tenant", me.Code, me.Body.String())
		}
		if i == 0 {
			if w := authRequest(h, "POST", "/api/auth/logout", testOrigin, "", "192.0.2.9", cookie); w.Code != 204 {
				t.Fatal(w.Code)
			}
		} else {
			if _, err := owner.ExecContext(ctx, "UPDATE browseai.users SET enabled=false WHERE login=$1", login); err != nil {
				t.Fatal(err)
			}
		}
		if w := authRequest(h, "GET", "/api/auth/me", "", "", "192.0.2.9", cookie); w.Code != http.StatusUnauthorized {
			t.Fatal("revoked or disabled session accepted", w.Code)
		}
	}
	for _, login := range []string{"mock-a", "unknown"} {
		body := `{"login":"` + login + `","password":"wrong"}`
		if w := authRequest(h, "POST", "/api/auth/login", testOrigin, body, "192.0.2.9", nil); w.Code != 401 {
			t.Fatal("invalid credential accepted", w.Code)
		}
	}
}
