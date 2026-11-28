package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
)

func TestGitHubCIAcceptanceConfiguration(t *testing.T) {
	c := githubCIAcceptanceConfig{
		Domain: githubCIAcceptanceConfigDomain,
		Policy: githubFailureConfig{
			Domain: githubFailureConfigDomain, Grant: "00000000-0000-4000-8000-000000009984",
			Tenant: "00000000-0000-4000-8000-000000000001", Task: "00000000-0000-4000-8000-000000000003",
			Project: "00000000-0000-4000-8000-000000000002", Revision: 4, Installation: 12, Repository: 34,
			FullName: "owner/repo", Branch: "feat/demo", HeadSHA: strings.Repeat("a", 40), Workflow: 56,
			Name: "CI", Path: ".github/workflows/ci.yml", Required: []string{"gate"}, Source: strings.Repeat("b", 64),
			PR: 3, BaseBranch: "main", BaseSHA: strings.Repeat("b", 40), AppIssuer: "12", Expires: "2026-11-28T12:00:00Z",
		},
		Actor: "00000000-0000-4000-8000-000000009983", Push: "00000000-0000-4000-8000-000000009981",
		PR: "00000000-0000-4000-8000-000000009982", PushDigest: strings.Repeat("c", 64), PRDigest: strings.Repeat("d", 64),
		Verifier: "00000000-0000-4000-8000-000000009951", Key: "00000000-0000-4000-8000-000000009952",
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeGitHubCIAcceptanceConfig(raw)
	if err != nil {
		t.Fatal("valid configuration rejected", err)
	}
	p, a, err := got.parameters()
	if err != nil || p.pr == nil || p.task.taskID != c.Policy.Task || a.approvalID != c.Policy.Grant ||
		a.actorID != c.Actor || a.prID != c.PR || a.pushID != c.Push ||
		hex.EncodeToString(a.pushDigest[:]) != c.PushDigest || hex.EncodeToString(a.prDigest[:]) != c.PRDigest {
		t.Fatal("configuration scope lost", err)
	}
	for _, change := range []func(*githubCIAcceptanceConfig){
		func(v *githubCIAcceptanceConfig) { v.Domain = "other" },
		func(v *githubCIAcceptanceConfig) { v.Policy.PR = 0; v.Policy.BaseBranch = ""; v.Policy.BaseSHA = "" },
		func(v *githubCIAcceptanceConfig) { v.Policy.BaseSHA = "" },
		func(v *githubCIAcceptanceConfig) { v.Actor = "" },
		func(v *githubCIAcceptanceConfig) { v.PR = v.Push },
		func(v *githubCIAcceptanceConfig) { v.PRDigest = v.PushDigest },
		func(v *githubCIAcceptanceConfig) { v.Key = "" },
		func(v *githubCIAcceptanceConfig) { v.Verifier = "" },
		func(v *githubCIAcceptanceConfig) { v.PushDigest = strings.Repeat("g", 64) },
		func(v *githubCIAcceptanceConfig) { v.Policy.Revision = -1 },
	} {
		bad := c
		change(&bad)
		buf, err := json.Marshal(bad)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeGitHubCIAcceptanceConfig(buf); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
	for _, bad := range [][]byte{nil, []byte("{}"), append(append([]byte(nil), raw...), '\n'), make([]byte, 65537)} {
		if _, err := decodeGitHubCIAcceptanceConfig(bad); err == nil {
			t.Fatal("invalid encoding accepted")
		}
	}
	if err := githubCIAcceptanceCommand(nil, nil); err == nil {
		t.Fatal("missing command arguments accepted")
	}
	if runtime.GOOS != "linux" || os.Geteuid() <= 0 {
		return
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "acceptance.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BROWSEAI_CI_ACCEPTOR_DSN", "")
	t.Setenv("BROWSEAI_CI_APPROVAL_DSN", "")
	for _, action := range []string{"unknown", "accept", "issue", "revoke"} {
		if err := githubCIAcceptanceCommand([]string{action, dir}, strings.NewReader("invalid")); err == nil {
			t.Fatal("unauthorized command accepted", action)
		}
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := githubCIAcceptanceCommand([]string{"accept", dir}, nil); err == nil {
		t.Fatal("public config accepted")
	}
}

func testGitHubCommittedAcceptance(t *testing.T, ctx context.Context, owner *sql.DB, verifier githubVerifierStore, pushApp, prApp *githubApp, policy githubIndependentPolicy, pushID, prID string, clock func() time.Time) {
	t.Helper()
	operator, err := sql.Open("postgres", os.Getenv("BROWSEAI_TEST_CI_APPROVAL_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer operator.Close()
	acceptor, err := sql.Open("postgres", os.Getenv("BROWSEAI_TEST_CI_ACCEPTOR_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer acceptor.Close()
	a := githubCIAcceptanceApproval{
		actorID: "00000000-0000-4000-8000-000000009983", approvalID: "00000000-0000-4000-8000-000000009979",
		tenantID: policy.execution.binding.tenantID, task: policy.task, pushID: pushID, prID: prID,
	}
	for _, entry := range []struct {
		id   string
		dest *[32]byte
	}{{pushID, &a.pushDigest}, {prID, &a.prDigest}} {
		var digest []byte
		if err := owner.QueryRowContext(ctx, "SELECT evidence_digest FROM browseai.github_ci_evidence WHERE id=$1", entry.id).Scan(&digest); err != nil || len(digest) != 32 {
			t.Fatal("digest fixture unavailable", err)
		}
		copy(entry.dest[:], digest)
	}
	if err := runGitHubCIAuthorizedAcceptance(ctx, owner, verifier, pushApp, prApp, policy, a, clock); err == nil {
		t.Fatal("owner accepted as runtime")
	}
	if err := runGitHubCIAuthorizedAcceptance(ctx, acceptor, verifier, pushApp, prApp, policy, a, clock); err == nil {
		t.Fatal("unapproved acceptance succeeded")
	}
	if err := changeGitHubCIAcceptanceGrant(ctx, operator, policy, a, "issue"); err != nil {
		t.Fatal("fresh grant issue", err)
	}
	if err := runGitHubCIAuthorizedAcceptance(ctx, acceptor, verifier, pushApp, prApp, policy, a, clock); err != nil {
		t.Fatal("committed acceptance failed", err)
	}
	if err := runGitHubCIAuthorizedAcceptance(ctx, acceptor, verifier, pushApp, prApp, policy, a, clock); err == nil {
		t.Fatal("committed acceptance replayed")
	}
	var state string
	var revision int64
	var count int
	var linked bool
	if err := owner.QueryRowContext(ctx, "SELECT t.state,t.revision,(SELECT count(*) FROM browseai.github_ci_evidence WHERE id IN($2,$3) AND consumed_at IS NOT NULL),g.acceptance_id=g.id FROM browseai.tasks t JOIN browseai.github_ci_acceptance_grants g ON g.task_id=t.id WHERE t.id=$1 AND g.id=$4", a.task.taskID, a.pushID, a.prID, a.approvalID).Scan(&state, &revision, &count, &linked); err != nil || state != "STAGE_PASSED" || revision != a.task.revision+1 || count != 2 || !linked {
		t.Fatal("committed acceptance not durable", state, revision, count, linked, err)
	}
	// Complete the accepted single-stage task only through the separate
	// historical decision, not through a monitor flag or a new CI read.
	if _, err := owner.ExecContext(ctx, "INSERT INTO browseai.stages(tenant_id,task_id,stage_id,ordinal,weight,instruction,acceptance) VALUES($1,$2,'stage',0,1,'committed fixture',ARRAY['CI'])", a.tenantID, a.task.taskID); err != nil {
		t.Fatal(err)
	}
	const decision = "00000000-0000-4000-8000-000000009980"
	query := "SELECT browseai.decide_github_stage($1,$2,$3,$4,$5,$6,$7)"
	args := []any{a.tenantID, a.task.taskID, revision, a.approvalID, a.actorID, decision, "complete"}
	tx, err := owner.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Successful evidence already pins the project/branch through a
	// composite foreign key. Assert the actual SQLSTATE, not any error.
	_, driftErr := tx.ExecContext(ctx, "UPDATE browseai.projects SET code_branch='feat/drift' WHERE id=$1", a.task.projectID)
	var fkErr *pq.Error
	if !errors.As(driftErr, &fkErr) || fkErr.Code != "23503" || fkErr.Table != "github_ci_evidence" {
		t.Fatal("expected immutable evidence foreign key", driftErr)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	// Independently verify that a live binding revoked after acceptance
	// prevents the final decision even with unchanged evidence.
	tx, err = owner.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "UPDATE browseai.github_repositories SET enabled=false WHERE tenant_id=$1 AND repository_id=$2", a.tenantID, policy.execution.binding.repositoryID); err != nil {
		t.Fatal(err)
	}
	var ok bool
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&ok); err != nil || ok {
		t.Fatal("historical decision accepted on disabled repository", ok, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	human, err := sql.Open("postgres", os.Getenv("BROWSEAI_TEST_STAGE_HUMAN_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer human.Close()
	if err := githubStageHumanReady(ctx, human); err != nil {
		t.Fatal("stage role unavailable", err)
	}
	if err := githubStageHumanReady(ctx, owner); err == nil {
		t.Fatal("owner accepted as stage runtime")
	}
	if err := human.QueryRowContext(ctx, query, args...).Scan(&ok); err == nil {
		t.Fatal("stage role can invoke sealed primitive")
	}
	raw := make([]byte, 32)
	raw[0] = 93
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest, err := digestSession(token)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRowContext(ctx, "SELECT browseai.issue_session($1,$2,$3)", a.actorID, a.tenantID, digest[:]).Scan(&ok); err != nil || !ok {
		t.Fatal("stage session fixture", ok, err)
	}
	for _, bad := range []struct {
		token, acceptance string
		revision          int64
		action            taskAction
		confirmed         bool
	}{
		{"", a.approvalID, revision, actionComplete, true},
		{token, decision, revision, actionComplete, true},
		{token, a.approvalID, revision + 1, actionComplete, true},
		{token, a.approvalID, revision, actionNext, true},
		{token, a.approvalID, revision, actionComplete, false},
	} {
		if err := reviewAuthorizedGitHubStage(ctx, human, bad.token, a.tenantID, a.task.taskID, bad.acceptance, decision, bad.revision, bad.action, bad.confirmed); err == nil {
			t.Fatal("invalid stage decision accepted")
		}
	}
	authDB, err := sql.Open("postgres", os.Getenv("BROWSEAI_TEST_AUTH_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer authDB.Close()
	auth, err := newAuthHTTP(testOrigin, sqlCredentialVerifier{authDB}, sessionService{store: sqlSessionStore{authDB}})
	if err != nil {
		t.Fatal(err)
	}
	handler := githubStageReviewRoutes(auth, human, http.NotFoundHandler())
	handoff := func(tok string) *httptest.ResponseRecorder {
		return authRequest(handler, "POST", "/api/tasks/"+a.task.taskID+"/github-stage/handoff", testOrigin, "", "192.0.2.1", &http.Cookie{Name: sessionCookie, Value: tok})
	}
	w := handoff(token)
	var summary githubStageHandoff
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &summary) != nil || summary.Acceptance != a.approvalID ||
		summary.Revision != revision || summary.PushID != a.pushID || summary.PRID != a.prID ||
		summary.PushDigest != hex.EncodeToString(a.pushDigest[:]) || summary.PRDigest != hex.EncodeToString(a.prDigest[:]) {
		t.Fatal("historical evidence handoff mismatch", w.Code)
	}
	if _, err := readGitHubStageHandoff(ctx, owner, token, a.tenantID, a.task.taskID); err == nil {
		t.Fatal("owner accepted as handoff role")
	}
	in := githubStageReviewRequest{Revision: revision, Acceptance: a.approvalID, Decision: decision, Reviewed: true}
	request := func(action, tok, origin string, input githubStageReviewRequest) int {
		body, err := json.Marshal(input)
		if err != nil {
			return 0
		}
		return authRequest(handler, "POST", "/api/tasks/"+a.task.taskID+"/github-stage/"+action, origin, string(body), "192.0.2.1", &http.Cookie{Name: sessionCookie, Value: tok}).Code
	}
	if request("complete", token, "https://foreign.example", in) != 403 || request("complete", "invalid", testOrigin, in) != 401 {
		t.Fatal("stage HTTP origin or session bypass")
	}
	if request("next", token, testOrigin, in) != 409 {
		t.Fatal("final stage advanced through HTTP")
	}
	unreviewed := in
	unreviewed.Reviewed = false
	if request("complete", token, testOrigin, unreviewed) != 400 {
		t.Fatal("unreviewed stage decision accepted")
	}
	if _, err := owner.ExecContext(ctx, "UPDATE browseai.auth_sessions SET revoked_at=clock_timestamp() WHERE token_digest=$1", digest[:]); err != nil {
		t.Fatal(err)
	}
	if handoff(token).Code != 401 || request("complete", token, testOrigin, in) != 401 {
		t.Fatal("revoked session accepted through stage HTTP")
	}
	raw[0] = 94
	token = base64.RawURLEncoding.EncodeToString(raw)
	digest, err = digestSession(token)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.QueryRowContext(ctx, "SELECT browseai.issue_session($1,$2,$3)", a.actorID, a.tenantID, digest[:]).Scan(&ok); err != nil || !ok {
		t.Fatal("replacement stage session fixture", ok, err)
	}
	var wg sync.WaitGroup
	results := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- request("complete", token, testOrigin, in)
		}()
	}
	wg.Wait()
	close(results)
	wins := 0
	for code := range results {
		if code == 204 {
			wins++
		} else if code != 409 {
			t.Fatal("unexpected stage HTTP race status", code)
		}
	}
	if wins != 1 {
		t.Fatal("expected one stage decision", wins)
	}
	if err := reviewAuthorizedGitHubStage(ctx, human, token, a.tenantID, a.task.taskID, a.approvalID, decision, revision, actionComplete, true); err == nil {
		t.Fatal("committed decision replayed")
	}
	if handoff(token).Code != 409 || request("complete", token, testOrigin, in) != 409 {
		t.Fatal("committed HTTP stage decision replayed")
	}
	if err := owner.QueryRowContext(ctx, "SELECT count(*) FROM browseai.github_stage_decisions d JOIN browseai.audit_logs l ON l.id=d.decision_id JOIN browseai.events e ON e.audit_id=l.id WHERE d.decision_id=$1 AND d.acceptance_id=$2 AND l.actor_id=$3 AND e.kind='task.complete'", decision, a.approvalID, a.actorID).Scan(&count); err != nil || count != 1 {
		t.Fatal("missing or duplicate decision audit", count, err)
	}
	if err := owner.QueryRowContext(ctx, "SELECT state,revision FROM browseai.tasks WHERE id=$1", a.task.taskID).Scan(&state, &revision); err != nil || state != "COMPLETED" || revision != a.task.revision+2 {
		t.Fatal("complete decision not durable", state, revision, err)
	}
}
