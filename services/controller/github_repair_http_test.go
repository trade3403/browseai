package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitHubRepairHTTP(t *testing.T) {
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
	h := githubRepairReviewRoutes(a, nil, http.NotFoundHandler())
	in := githubRepairReviewRequest{Revision: 4, Fence: 1, Grant: task, Failure: actor, Approval: tenant, Instruction: []byte("repair fixture"), Reviewed: true}
	body, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/tasks/" + task + "/github-repair/approve"
	for _, tc := range []struct {
		path   string
		token  string
		origin string
		body   string
		code   int
	}{
		{path, admin, "https://other.example", string(body), 403},
		{path, "", testOrigin, string(body), 401},
		{path, user, testOrigin, string(body), 403},
		{path + "?tenant=other", admin, testOrigin, string(body), 400},
		{strings.Replace(path, task, "bad", 1), admin, testOrigin, string(body), 400},
		{path, admin, testOrigin, "{}", 400},
		{path, admin, testOrigin, string(body) + "{}", 400},
		{path, admin, testOrigin, strings.Replace(string(body), "{", "{\"actor_id\":\"fake\",", 1), 400},
		{path, admin, testOrigin, strings.Repeat("x", 33000), 400},
		{path, admin, testOrigin, string(body), 409},
	} {
		w := authRequest(h, "POST", tc.path, tc.origin, tc.body, "192.0.2.1", &http.Cookie{Name: sessionCookie, Value: tc.token})
		if w.Code != tc.code || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("unexpected review response", tc.code, w.Code)
		}
	}
	if !in.valid("approve") || in.valid("confirm_sent") || in.valid("unknown") {
		t.Fatal("wrong action contract")
	}
	in.Confirmation = actor
	in.ManualSent = true
	if !in.valid("confirm_sent") || in.valid("approve") {
		t.Fatal("wrong confirmation contract")
	}
	in.Confirmation = in.Approval
	if in.valid("confirm_sent") {
		t.Fatal("duplicate receipt accepted")
	}
	{
		h.ServeHTTP(w, httptest.NewRequest("GET", "/readyz", nil))
		if w.Code != 503 {
			t.Fatal("missing review database ready")
		}
	}
	if _, err := openGitHubRepairReviewRuntime(context.Background(), nil, nil, "", testOrigin); err == nil {
		t.Fatal("invalid runtime accepted")
	}
}
