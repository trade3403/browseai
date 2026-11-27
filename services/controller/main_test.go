package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
	routes().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/projects", nil))
	var body struct {
		Mock bool `json:"mock"`
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
	for _, tc := range []struct{ method, path string; code int }{
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
