package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubStageHTTP(t *testing.T) {
	a, _ := testAuth(t, testVerifier{})
	const tenant = "00000000-0000-4000-8000-000000000001"
	const task = "00000000-0000-4000-8000-000000000002"
	const actor = "00000000-0000-4000-8000-000000000003"
	admin, err := a.sessions.issueVerified(context.Background(), principal{actor, tenant, rolePlatformAdmin})
	if err != nil {
		t.Fatal(err)
	}
	user, err := a.sessions.issueVerified(context.Background(), principal{actor, tenant, roleUser})
	if err != nil {
		t.Fatal(err)
	}
	h := githubStageReviewRoutes(a, nil, http.NotFoundHandler())
	body := `{"revision":2,"acceptance_id":"00000000-0000-4000-8000-000000000004","decision_id":"00000000-0000-4000-8000-000000000005","reviewed":true}`
	path := "/api/tasks/" + task + "/github-stage/complete"
	for _, tc := range []struct {
		path   string
		token  string
		origin string
		body   string
		code   int
	}{
		{path, admin, "https://other.example", body, 403},
		{path, "", testOrigin, body, 401},
		{path, user, testOrigin, body, 403},
		{path + "?tenant=other", admin, testOrigin, body, 400},
		{strings.Replace(path, task, "bad", 1), admin, testOrigin, body, 400},
		{path, admin, testOrigin, "{}", 400},
		{path, admin, testOrigin, body + "{}", 400},
		{path, admin, testOrigin, strings.Replace(body, "{", "{\"actor_id\":\"fake\",", 1), 400},
		{path, admin, testOrigin, strings.Replace(body, "true", "false", 1), 400},
		{path, admin, testOrigin, strings.Repeat("x", 5000), 400},
		{path, admin, testOrigin, body, 409},
		{strings.Replace(path, "complete", "next", 1), admin, testOrigin, body, 409},
	} {
		w := authRequest(h, "POST", tc.path, tc.origin, tc.body, "192.0.2.1", &http.Cookie{Name: sessionCookie, Value: tc.token})
		if w.Code != tc.code || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("unexpected stage review response", tc.code, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/readyz", nil))
	if w.Code != 503 {
		t.Fatal("missing stage database ready")
	}
	if _, err := openGitHubStageReviewRuntime(context.Background(), nil, nil, "", testOrigin); err == nil {
		t.Fatal("invalid runtime accepted")
	}
}
