package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
)

// The pure state machine remains reusable, but its legacy boolean evidence
// must not directly authorize any of the sealed persistence transitions.
func TestLegacyGitHubTransitionsDenied(t *testing.T) {
	for _, action := range []taskAction{actionCIPass, actionCIFail, actionRepair, actionNext, actionComplete} {
		if _, err := (taskStore{}).transition(context.Background(), principal{}, "", 0, action, taskEvidence{
			CIComplete: true, CIAllPassed: true, Acceptance: true, ReadyToRun: true, FinalAccepted: true,
		}); !errors.Is(err, errResourceConflict) {
			t.Fatal("legacy transition not blocked", action, err)
		}
	}
}

// Real PostgreSQL connections race the same immutable input. All successful
// calls commit, so a later round observes the actual durable winner.
func testGitHubRepairRace(t *testing.T, ctx context.Context, db *sql.DB, c githubCIFailureCandidate) {
	t.Helper()
	tenant, task := c.policy.binding.tenantID, c.task.taskID
	const actor = "00000000-0000-4000-8000-000000009971"
	const approval = "00000000-0000-4000-8000-000000009972"
	const confirmation = "00000000-0000-4000-8000-000000009973"
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.tasks(id,tenant_id,project_id,name,state,stage_count,code_sha,revision,max_repairs) VALUES($1,$2,$3,'repair race fixture','WAIT_CI',1,$4,$5,1)", task, tenant, c.task.projectID, c.policy.sha, c.task.revision); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.stages(tenant_id,task_id,stage_id,ordinal,weight,instruction,acceptance) VALUES($1,$2,'stage',0,1,'fixture',ARRAY['CI'])", tenant, task); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.users(id,login,password_hash) VALUES($1,'ci-race-fixture','synthetic')", actor); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.memberships(tenant_id,user_id,role) VALUES($1,$2,'platform_admin')", tenant, actor); err != nil {
		t.Fatal(err)
	}
	var fence int64
	if err := tx.QueryRowContext(ctx, "INSERT INTO browseai.leases(repository_id,code_branch,tenant_id,task_id,project_id,expires_at) VALUES($1,$2,$3,$4,$5,clock_timestamp()+interval '60 seconds') RETURNING fence", c.policy.binding.repositoryID, c.policy.branch, tenant, task, c.task.projectID).Scan(&fence); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	grant := testGitHubFailureGrantOperator(t, ctx, db, c)
	observerDSN := os.Getenv("BROWSEAI_TEST_FAILURE_OBSERVER_DSN")
	if observerDSN == "" {
		t.Fatal("missing observer fixture")
	}
	observer, err := sql.Open("postgres", observerDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer observer.Close()
	if err := githubFailureObserverReady(ctx, observer); err != nil {
		t.Fatal("observer role rejected", err)
	}
	if err := githubFailureObserverReady(ctx, db); err == nil {
		t.Fatal("owner accepted as observer")
	}
	var denied sql.NullString
	if err := observer.QueryRowContext(ctx, "SELECT browseai.record_github_ci_failure($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14::bigint[],$15)::text", tenant, task, c.task.revision, c.task.projectID, c.policy.binding.installationID, c.policy.binding.repositoryID, c.policy.branch, c.policy.sha, c.policy.workflowID, c.source, c.event, c.runID, c.attempt, "{1}", c.observedAt).Scan(&denied); err == nil {
		t.Fatal("observer can bypass grant")
	}
	defer func() {
		if _, err := db.ExecContext(ctx, "DELETE FROM browseai.leases WHERE tenant_id=$1 AND task_id=$2 AND fence=$3", tenant, task, fence); err != nil {
			t.Error("fixture lease cleanup", err)
		}
	}()
	race := func(name string, call func() (bool, error)) {
		t.Helper()
		type result struct {
			ok  bool
			err error
		}
		start := make(chan struct{})
		out := make(chan result, 8)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				ok, err := call()
				out <- result{ok, err}
			}()
		}
		close(start)
		wg.Wait()
		close(out)
		winners := 0
		for r := range out {
			if r.err != nil {
				t.Fatal(name, r.err)
			}
			if r.ok {
				winners++
			}
		}
		if winners != 1 {
			t.Fatal(name, "expected exactly one winner", winners)
		}
	}
	// Reject a wrong grant and a different pinned workflow before racing.
	if _, err := recordAuthorizedGitHubFailure(ctx, observer, c, "00000000-0000-4000-8000-000000009999", func() time.Time { return c.observedAt }); err == nil {
		t.Fatal("unknown grant accepted")
	}
	changed := c
	changed.policy.workflowID++
	if _, err := recordAuthorizedGitHubFailure(ctx, observer, changed, grant, func() time.Time { return c.observedAt }); err == nil {
		t.Fatal("changed workflow accepted")
	}
	race("record authorized failure", func() (bool, error) {
		var id sql.NullString
		err := observer.QueryRowContext(ctx, githubAuthorizedFailureSQL, grant, tenant, task, c.task.revision, c.task.projectID,
			c.policy.binding.installationID, c.policy.binding.repositoryID, c.policy.branch, c.policy.sha,
			c.policy.workflowID, c.source, c.event, c.runID, c.attempt, pq.Array(c.failedIDs), c.observedAt).Scan(&id)
		return id.Valid, err
	})
	var failure string
	if err := db.QueryRowContext(ctx, "SELECT id::text FROM browseai.github_ci_failures WHERE tenant_id=$1 AND task_id=$2", tenant, task).Scan(&failure); err != nil {
		t.Fatal(err)
	}
	var linked bool
	if err := db.QueryRowContext(ctx, "SELECT failure_id=$2 FROM browseai.github_failure_observer_grants WHERE id=$1", grant, failure).Scan(&linked); err != nil || !linked {
		t.Fatal("grant consumption missing", linked, err)
	}
	// Temporarily move both the project and lease, keeping the same
	// task, SHA, repository and fence. No old failure may authorize it.
	rejectBranchDrift := func(action string, call func(*sql.Tx) (bool, error)) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err := tx.ExecContext(ctx, "DELETE FROM browseai.leases WHERE fence=$1", fence); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE browseai.projects SET code_branch='feat/drift' WHERE id=$1", c.task.projectID); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO browseai.leases(repository_id,code_branch,tenant_id,task_id,project_id,fence,expires_at) VALUES($1,'feat/drift',$2,$3,$4,$5,clock_timestamp()+interval '60 seconds')", c.policy.binding.repositoryID, tenant, task, c.task.projectID, fence); err != nil {
			t.Fatal(err)
		}
		if ok, err := call(tx); err != nil || ok {
			t.Fatal("branch drift", action, ok, err)
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
	var branch, project string
	if err := db.QueryRowContext(ctx, "SELECT code_branch,project_id::text FROM browseai.github_ci_failures WHERE id=$1", failure).Scan(&branch, &project); err != nil || branch != c.policy.branch || project != c.task.projectID {
		t.Fatal("original failure scope lost", branch, project, err)
	}
	rejectBranchDrift("reserve", func(tx *sql.Tx) (bool, error) {
		var target sql.NullString
		err := tx.QueryRowContext(ctx, "SELECT browseai.reserve_github_ci_repair($1,$2,$3,$4,$5)", tenant, task, c.task.revision+1, failure, fence).Scan(&target)
		return target.Valid, err
	})
	reserver, err := sql.Open("postgres", os.Getenv("BROWSEAI_TEST_REPAIR_RESERVER_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer reserver.Close()
	if err := githubRepairReserverReady(ctx, reserver); err != nil {
		t.Fatal("reserver role rejected", err)
	}
	if err := githubRepairReserverReady(ctx, db); err == nil {
		t.Fatal("owner accepted as reserver")
	}
	if _, err := reserveAuthorizedGitHubRepair(ctx, reserver, "00000000-0000-4000-8000-000000009999", tenant, task, failure, c.task.revision+1, fence); err == nil {
		t.Fatal("unrelated grant accepted")
	}
	if _, err := reserveAuthorizedGitHubRepair(ctx, reserver, grant, tenant, task, failure, c.task.revision+1, fence+1); err == nil {
		t.Fatal("wrong fence accepted")
	}
	if _, err := reserveAuthorizedGitHubRepair(ctx, reserver, grant, tenant, task, failure, 0, fence); err == nil {
		t.Fatal("invalid revision accepted")
	}
	if _, err := db.ExecContext(ctx, "UPDATE browseai.stages SET dependencies=ARRAY['stage'] WHERE tenant_id=$1 AND task_id=$2", tenant, task); err != nil {
		t.Fatal(err)
	}
	if _, err := reserveAuthorizedGitHubRepair(ctx, reserver, grant, tenant, task, failure, c.task.revision+1, fence); err == nil {
		t.Fatal("cyclic stage plan accepted")
	}
	if _, err := db.ExecContext(ctx, "UPDATE browseai.stages SET dependencies=ARRAY[]::text[] WHERE tenant_id=$1 AND task_id=$2", tenant, task); err != nil {
		t.Fatal(err)
	}
	race("reserve authorized repair", func() (bool, error) {
		var target sql.NullString
		err := reserver.QueryRowContext(ctx, githubRepairReserverSQL, grant, tenant, task, c.task.revision+1, failure, fence).Scan(&target)
		if err == nil && target.Valid && target.String != "AWAIT_APPROVAL" {
			return false, errors.New("unexpected repair target")
		}
		return target.Valid, err
	})
	if _, err := reserveAuthorizedGitHubRepair(ctx, reserver, grant, tenant, task, failure, c.task.revision+1, fence); err == nil {
		t.Fatal("reservation replay accepted")
	}
	var reservedBy string
	if err := db.QueryRowContext(ctx, "SELECT observer_role FROM browseai.github_ci_repair_audit WHERE failure_id=$1 AND action='repair_reserved'", failure).Scan(&reservedBy); err != nil || reservedBy != "browseai_repair_reserver" {
		t.Fatal("reservation identity missing", reservedBy, err)
	}
	instruction := []byte("Review and repair race fixture")
	rejectBranchDrift("approve", func(tx *sql.Tx) (bool, error) {
		var ok bool
		err := tx.QueryRowContext(ctx, "SELECT browseai.approve_github_ci_repair($1,$2,$3,$4,$5,$6,$7,$8)", tenant, task, c.task.revision+2, failure, actor, approval, fence, instruction).Scan(&ok)
		return ok, err
	})
	human, err := sql.Open("postgres", os.Getenv("BROWSEAI_TEST_REPAIR_HUMAN_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer human.Close()
	// Use synthetic sessions issued by the same database authentication
	// primitive used by the application. The runtime never receives actor IDs.
	raw := make([]byte, 32)
	raw[0] = 71
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest, err := digestSession(token)
	if err != nil {
		t.Fatal(err)
	}
	var issued bool
	if err := db.QueryRowContext(ctx, "SELECT browseai.issue_session($1,$2,$3)", actor, tenant, digest[:]).Scan(&issued); err != nil || !issued {
		t.Fatal("session fixture", issued, err)
	}
	review := func(tok, action, receipt string, sent bool, bytes []byte) error {
		return reviewAuthorizedGitHubRepair(ctx, human, tok, tenant, task, grant, failure, approval, receipt, c.task.revision+2, fence, bytes, action, sent)
	}
	if err := review("invalid", "approve", "", false, instruction); err == nil {
		t.Fatal("invalid session accepted")
	}
	if err := review(token, "confirm_sent", confirmation, true, instruction); err == nil {
		t.Fatal("confirmation before approval accepted")
	}
	if err := review(token, "approve", "", true, instruction); err == nil {
		t.Fatal("approval conflated with send")
	}
	if _, err := db.ExecContext(ctx, "UPDATE browseai.memberships SET role='user' WHERE user_id=$1 AND tenant_id=$2", actor, tenant); err != nil {
		t.Fatal(err)
	}
	if err := review(token, "approve", "", false, instruction); err == nil {
		t.Fatal("ordinary user approved")
	}
	if _, err := db.ExecContext(ctx, "UPDATE browseai.memberships SET role='platform_admin' WHERE user_id=$1 AND tenant_id=$2", actor, tenant); err != nil {
		t.Fatal(err)
	}
	// Use the real authentication database identity for HTTP requests.
	authDB, err := sql.Open("postgres", os.Getenv("BROWSEAI_TEST_AUTH_DSN"))
	if err != nil {
		t.Fatal(err)
	}
	defer authDB.Close()
	if err := githubRepairHumanReady(ctx, human); err != nil {
		t.Fatal("review role rejected", err)
	}
	if err := githubRepairHumanReady(ctx, db); err == nil {
		t.Fatal("owner accepted as review runtime")
	}
	auth, err := newAuthHTTP(testOrigin, sqlCredentialVerifier{authDB}, sessionService{store: sqlSessionStore{authDB}})
	if err != nil {
		t.Fatal(err)
	}
	handler := githubRepairReviewRoutes(auth, human, http.NotFoundHandler())
	handoff := func(tok string) (githubRepairHandoff, int) {
		w := authRequest(handler, "POST", "/api/tasks/"+task+"/github-repair/handoff", testOrigin, "", "192.0.2.1", &http.Cookie{Name: sessionCookie, Value: tok})
		var out githubRepairHandoff
		if w.Code == 200 && json.Unmarshal(w.Body.Bytes(), &out) != nil {
			return out, 0
		}
		return out, w.Code
	}
	if _, err := readGitHubRepairHandoff(ctx, human, token, "00000000-0000-4000-8000-000000000000", task); err == nil {
		t.Fatal("repair handoff accepted foreign tenant")
	}
	if _, err := readGitHubRepairHandoff(ctx, human, token, tenant, approval); err == nil {
		t.Fatal("repair handoff accepted unrelated task")
	}
	if _, err := readGitHubRepairHandoff(ctx, db, token, tenant, task); err == nil {
		t.Fatal("owner accepted as repair handoff role")
	}
	summary, code := handoff(token)
	if code != 200 || summary.Grant != grant || summary.Failure != failure || summary.Fence != fence || summary.Revision != c.task.revision+2 || summary.Approval != nil {
		t.Fatal("repair handoff mismatch", code)
	}

	in := githubRepairReviewRequest{Revision: c.task.revision + 2, Fence: fence, Grant: grant, Failure: failure, Approval: approval, Instruction: instruction, Reviewed: true}
	request := func(action, tok, origin string, input githubRepairReviewRequest) int {
		body, err := json.Marshal(input)
		if err != nil {
			return 0
		}
		return authRequest(handler, "POST", "/api/tasks/"+task+"/github-repair/"+action, origin, string(body), "192.0.2.1", &http.Cookie{Name: sessionCookie, Value: tok}).Code
	}
	if request("approve", token, "https://foreign.example", in) != 403 || request("approve", "invalid", testOrigin, in) != 401 {
		t.Fatal("HTTP origin or session bypass")
	}
	unreviewed := in
	unreviewed.Reviewed = false
	if request("approve", token, testOrigin, unreviewed) != 400 {
		t.Fatal("unreviewed HTTP approval accepted")
	}
	race("approve repair", func() (bool, error) {
		code := request("approve", token, testOrigin, in)
		if code != 204 && code != 409 {
			return false, errors.New("unexpected HTTP approval status")
		}
		return code == 204, nil
	})
	rejectBranchDrift("confirm", func(tx *sql.Tx) (bool, error) {
		var ok bool
		err := tx.QueryRowContext(ctx, "SELECT browseai.confirm_github_ci_repair($1,$2,$3,$4,$5,$6,$7,$8)", tenant, task, c.task.revision+2, approval, actor, confirmation, fence, instruction).Scan(&ok)
		return ok, err
	})
	summary, code = handoff(token)
	reviewedDigest := sha256.Sum256(instruction)
	if code != 200 || summary.Approval == nil || *summary.Approval != approval || summary.InstructionDigest == nil || *summary.InstructionDigest != hex.EncodeToString(reviewedDigest[:]) {
		t.Fatal("approved repair handoff mismatch", code)
	}
	if err := review(token, "approve", "", false, instruction); err == nil {
		t.Fatal("approval replay accepted")
	}
	if err := review(token, "confirm_sent", confirmation, false, instruction); err == nil {
		t.Fatal("unconfirmed send accepted")
	}
	if err := review(token, "confirm_sent", confirmation, true, []byte("changed")); err == nil {
		t.Fatal("changed instruction accepted")
	}
	if _, err := db.ExecContext(ctx, "UPDATE browseai.auth_sessions SET revoked_at=clock_timestamp() WHERE token_digest=$1", digest[:]); err != nil {
		t.Fatal(err)
	}
	if err := review(token, "confirm_sent", confirmation, true, instruction); err == nil {
		t.Fatal("revoked session accepted")
	}
	if _, code := handoff(token); code != 401 {
		t.Fatal("revoked session read repair handoff", code)
	}
	raw[0] = 72
	token = base64.RawURLEncoding.EncodeToString(raw)
	digest, err = digestSession(token)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, "SELECT browseai.issue_session($1,$2,$3)", actor, tenant, digest[:]).Scan(&issued); err != nil || !issued {
		t.Fatal("replacement session fixture", issued, err)
	}
	in.Confirmation = confirmation
	in.ManualSent = true
	race("confirm repair", func() (bool, error) {
		code := request("confirm-sent", token, testOrigin, in)
		if code != 204 && code != 409 {
			return false, errors.New("unexpected HTTP confirmation status")
		}
		return code == 204, nil
	})
	if _, code := handoff(token); code != 409 {
		t.Fatal("completed repair still available", code)
	}
	var state string
	var revision int64
	var repairs, audit, approvals, confirmations int
	err = db.QueryRowContext(ctx, "SELECT state,revision,repairs,(SELECT count(*) FROM browseai.github_ci_repair_audit WHERE failure_id=$2),(SELECT count(*) FROM browseai.github_ci_repair_approvals WHERE failure_id=$2),(SELECT count(*) FROM browseai.github_ci_repair_confirmations WHERE approval_id=$3) FROM browseai.tasks WHERE id=$1", task, failure, approval).Scan(&state, &revision, &repairs, &audit, &approvals, &confirmations)
	if err != nil || state != "DEVELOPING" || revision != c.task.revision+3 || repairs != 1 || audit != 2 || approvals != 1 || confirmations != 1 {
		t.Fatal("durable race result", state, revision, repairs, audit, approvals, confirmations, err)
	}
}
