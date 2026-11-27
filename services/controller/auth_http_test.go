package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type testVerifier struct {
	err error
}

func (v testVerifier) verify(_ context.Context, login, password string) (principal, error) {
	if v.err != nil {
		return principal{}, v.err
	}
	if login != "test-a" || password != "synthetic-only" {
		return principal{}, errUnauthenticated
	}
	return principal{"user-a", "tenant-a", roleUser}, nil
}

const testOrigin = "https://browseai.example"

func testAuth(t *testing.T, v credentialVerifier) (*authHTTP, *fakeSessions) {
	t.Helper()
	f := &fakeSessions{rows: make(map[sessionDigest]principal)}
	a, err := newAuthHTTP(testOrigin, v, sessionService{store: f})
	if err != nil {
		t.Fatal(err)
	}
	return a, f
}

func authRequest(h http.Handler, method, path, origin, body, peer string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, testOrigin+path, strings.NewReader(body))
	r.RemoteAddr = peer + ":12345"
	r.Header.Set("Origin", origin)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Tenant-ID", "tenant-b")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const testLoginBody = `{"login":"test-a","password":"synthetic-only"}`

func TestAuthHTTP(t *testing.T) {
	a, f := testAuth(t, testVerifier{})
	h := a.handler()
	request := func(method, path, origin, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		return authRequest(h, method, path, origin, body, "192.0.2.1", cookie)
	}
	login := request("POST", "/api/auth/login", testOrigin, testLoginBody, nil)
	if login.Code != 204 || login.Body.Len() != 0 || login.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("missing cookie")
	}
	cookie := cookies[0]
	if cookie.Name != sessionCookie || !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 28800 {
		t.Fatal("unsafe cookie")
	}
	me := request("GET", "/api/auth/me", "", "", cookie)
	if me.Code != 200 || !strings.Contains(me.Body.String(), "tenant-a") || strings.Contains(me.Body.String(), "tenant-b") {
		t.Fatal(me.Code, me.Body.String())
	}
	if w := request("POST", "/api/auth/logout", "https://other.example", "", cookie); w.Code != 403 {
		t.Fatal(w.Code)
	}
	rotated := request("POST", "/api/auth/login", testOrigin, testLoginBody, cookie)
	if rotated.Code != 204 || len(f.rows) != 1 {
		t.Fatal(rotated.Code, len(f.rows))
	}
	if w := request("GET", "/api/auth/me", "", "", cookie); w.Code != 401 {
		t.Fatal("old cookie remains valid")
	}
	cookie = rotated.Result().Cookies()[0]
	logout := request("POST", "/api/auth/logout", testOrigin, "", cookie)
	if logout.Code != 204 || logout.Result().Cookies()[0].MaxAge != -1 || len(f.rows) != 0 {
		t.Fatal(logout.Code, len(f.rows))
	}
	if w := request("GET", "/api/auth/me", "", "", cookie); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request("GET", "/api/auth/me", "", "", nil); w.Code != 401 {
		t.Fatal(w.Code)
	}
}

func TestAuthHTTPRejectsRequests(t *testing.T) {
	a, _ := testAuth(t, testVerifier{})
	h := a.handler()
	cases := []struct {
		origin string
		body   string
		status int
	}{
		{"https://other.example", testLoginBody, 403},
		{"", testLoginBody, 403},
		{testOrigin, `{"login":"test-a","password":"synthetic-only","tenant_id":"tenant-b"}`, 400},
		{testOrigin, testLoginBody + "{}", 400},
		{testOrigin, `{"login":"test-a","password":"wrong"}`, 401},
		{testOrigin, strings.Repeat("x", 5000), 400},
	}
	for i, tc := range cases {
		w := authRequest(h, "POST", "/api/auth/login", tc.origin, tc.body, "192.0.2."+string(rune('1'+i)), nil)
		if w.Code != tc.status {
			t.Fatal(tc.status, w.Code, w.Body.String())
		}
	}
	for i := 0; i < 6; i++ {
		w := authRequest(h, "POST", "/api/auth/login", testOrigin, `{"login":"test-a","password":"wrong"}`, "192.0.2.99", nil)
		if (i < 5 && w.Code != 401) || (i == 5 && w.Code != 429) {
			t.Fatal(i, w.Code)
		}
	}
	req := httptest.NewRequest("POST", testOrigin+"/api/auth/login", strings.NewReader(testLoginBody))
	req.Header.Set("Origin", testOrigin)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 415 {
		t.Fatal(w.Code)
	}
}

func TestAuthFailureAndConfig(t *testing.T) {
	a, f := testAuth(t, testVerifier{err: errors.New("database failure")})
	w := authRequest(a.handler(), "POST", "/api/auth/login", testOrigin, testLoginBody, "192.0.2.1", nil)
	if w.Code != 503 || strings.Contains(w.Body.String(), "database failure") || len(f.rows) != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, origin := range []string{"http://insecure.example", "https://example.com/path", "https://user@example.com", "https://example.com?a=b", "https://example.com#x"} {
		if _, err := newAuthHTTP(origin, testVerifier{}, sessionService{store: f}); err == nil {
			t.Fatal("accepted unsafe origin", origin)
		}
	}
}

func TestLoginLimiterGlobalAndReset(t *testing.T) {
	l := &loginLimiter{}
	now := time.Now()
	for i := 0; i < 60; i++ {
		if !l.allow(string(rune(i+100)), now) {
			t.Fatal("unexpected denial", i)
		}
	}
	if l.allow("new", now) || !l.allow("new", now.Add(time.Minute)) {
		t.Fatal("global limit or reset failed")
	}
}
