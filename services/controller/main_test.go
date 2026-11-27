package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHealth(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz"} {
		w := httptest.NewRecorder()
		routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		var body map[string]string
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body["status"] != "ok" || body["mode"] != "mock" {
			t.Fatalf("bad health response: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestProjects(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/projects?tenant_id=other", nil)
	r.Header.Set("X-Tenant-ID", "other")
	r.Header.Set("X-Role", "platform_admin")
	routes().ServeHTTP(w, r)
	var body struct {
		Mock     bool      `json:"mock"`
		Projects []Project `json:"projects"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || !body.Mock || len(body.Projects) != 6 || body.Projects[0].ID != "localtunnel" {
		t.Fatalf("unexpected projects: %+v", body)
	}
}

func TestUnknownAndWrite(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{http.MethodGet, "/missing", 404},
		{http.MethodPost, "/api/projects", 405},
	} {
		w := httptest.NewRecorder()
		routes().ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.code {
			t.Fatalf("%s: got %d want %d", tc.path, w.Code, tc.code)
		}
	}
}

func TestServerConfiguration(t *testing.T) {
	for _, tc := range []struct{ input, want string }{{"", "127.0.0.1:8080"}, {"127.0.0.1:18080", "127.0.0.1:18080"}} {
		server := newServer(tc.input)
		if server.Addr != tc.want || server.ReadHeaderTimeout != 5*time.Second || server.ReadTimeout != 10*time.Second || server.WriteTimeout != 10*time.Second || server.IdleTimeout != 30*time.Second {
			t.Fatalf("unexpected server config: %+v", server)
		}
		w := httptest.NewRecorder()
		server.Handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("health: %d", w.Code)
		}
	}
}

func TestJSONResponseHeadersAndEncodingError(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSON(w, map[string]string{"status": "ok"})
	if w.Header().Get("Content-Type") != "application/json; charset=utf-8" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("missing JSON response headers")
	}
	bad := httptest.NewRecorder()
	writeJSON(bad, func() {})
	if bad.Body.Len() != 0 {
		t.Fatal("invalid JSON must not be emitted")
	}
}
