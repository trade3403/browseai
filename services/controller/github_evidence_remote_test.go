package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGitHubEvidenceRemoteSnapshots(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	head, base, merge := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	p := githubPRPolicy{
		push: githubEvidencePolicy{
			binding: githubBinding{"00000000-0000-4000-8000-000000000001", 99532, 99533, "owner/repo"},
			branch:  "feat/demo", sha: head, workflowID: 2,
			name: "CI", path: ".github/workflows/ci.yml", required: []string{"gate"},
		},
		number: 3, baseBranch: "main", baseSHA: base,
	}
	headRef := `{"ref":"feat/demo","sha":"` + head + `","repo":{"id":99533}}`
	baseRef := `{"ref":"main","sha":"` + base + `","repo":{"id":99533}}`
	pr := `{"number":3,"state":"open","merged":false,"mergeable":true,"merge_commit_sha":"` + merge + `","head":` + headRef + `,"base":` + baseRef + `}`
	link := `{"number":3,"head":` + headRef + `,"base":` + baseRef + `}`
	run := `{"id":81,"workflow_id":2,"run_attempt":2,"head_sha":"` + head + `","head_branch":"feat/demo","event":"pull_request","status":"completed","conclusion":"success","repository":{"id":99533},"head_repository":{"id":99533},"path":".github/workflows/ci.yml","pull_requests":[` + link + `]}`
	workflow := `{"total_count":1,"workflows":[{"id":2,"name":"CI","path":".github/workflows/ci.yml","state":"active"}]}`
	job := `{"total_count":1,"jobs":[{"id":91,"run_id":81,"run_attempt":2,"head_sha":"` + head + `","name":"gate","status":"completed","conclusion":"success"}]}`
	const source = "name: CI\non: push\n"
	sum := sha256.Sum256([]byte(source))
	digest := hex.EncodeToString(sum[:])
	mode := ""
	prReads, listReads, detailReads := 0, 0, 0
	a, err := newGitHubApp("mock-app", key, githubTestTransport(func(r *http.Request) (*http.Response, error) {
		var body string
		status := 200
		switch r.URL.Path {
		case "/app/installations/99532/access_tokens":
			status = 201
			body = `{"token":"synthetic","expires_at":"` + now.Add(time.Hour).Format(time.RFC3339) + `","repositories":[{"id":99533}],"permissions":{"actions":"read","checks":"read","contents":"read","pull_requests":"read","metadata":"read"}}`
		case "/repos/owner/repo":
			body = `{"id":99533,"full_name":"owner/repo"}`
		case "/repos/owner/repo/contents/.github/workflows/ci.yml":
			text := source
			if mode == "source-changed" {
				text = "changed"
			}
			raw, err := json.Marshal(githubWorkflowSource{Type: "file", Path: p.push.path, Size: len(text), Encoding: "base64", Content: base64.StdEncoding.EncodeToString([]byte(text))})
			if err != nil {
				t.Fatal(err)
			}
			body = string(raw)
		case "/repos/owner/repo/pulls/3":
			prReads++
			body = pr
			if mode == "pr-changed" && prReads == 2 {
				body = strings.Replace(body, `"state":"open"`, `"state":"closed"`, 1)
			}
			if mode == "pr-missing" {
				status = 404
			}
		case "/repos/owner/repo/git/commits/" + merge:
			body = `{"sha":"` + merge + `","parents":[{"sha":"` + base + `"},{"sha":"` + head + `"}]}`
			if mode == "merge-error" {
				status = 503
			}
		case "/repos/owner/repo/actions/workflows":
			body = workflow
		case "/repos/owner/repo/actions/runs":
			listReads++
			if r.URL.Query().Get("head_sha") != head || r.URL.Query().Get("branch") != "feat/demo" {
				t.Fatal("wrong run query")
			}
			item := run
			if mode == "push" {
				item = strings.Replace(item, `"pull_request"`, `"push"`, 1)
			}
			body = `{"total_count":1,"workflow_runs":[` + item + `]}`
			if mode == "ambiguous" || (mode == "new-run" && listReads == 2) {
				body = `{"total_count":2,"workflow_runs":[` + item + "," + strings.Replace(item, `"id":81`, `"id":82`, 1) + `]}`
			}
		case "/repos/owner/repo/actions/runs/81":
			detailReads++
			body = run
			if mode == "push" {
				body = strings.Replace(body, `"pull_request"`, `"push"`, 1)
			}
			if mode == "foreign-link" {
				body = strings.Replace(body, `"number":3`, `"number":4`, 1)
			}
			if mode == "late-link" && detailReads == 4 {
				body = strings.Replace(body, `"number":3`, `"number":4`, 1)
			}
			if mode == "wrong-id" {
				body = strings.Replace(body, `"id":81`, `"id":82`, 1)
			}
		case "/repos/owner/repo/actions/runs/81/attempts/2/jobs":
			body = job
			if mode == "jobs-error" {
				status = 503
			}
		default:
			t.Fatal("unexpected endpoint", r.URL.Path)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	clock := func() time.Time { return now }
	candidate, err := a.observePRCandidate(ctx, p, clock)
	if err != nil || candidate.relationship.mergeSHA != merge || candidate.runID != 81 || len(candidate.jobIDs) != 1 || prReads != 4 || listReads != 2 || detailReads != 4 {
		t.Fatal("valid PR snapshot rejected", err, prReads, listReads, detailReads)
	}
	for _, bad := range []string{"pr-changed", "pr-missing", "merge-error", "ambiguous", "new-run", "foreign-link", "late-link", "wrong-id", "jobs-error"} {
		t.Run(bad, func(t *testing.T) {
			mode = bad
			prReads, listReads, detailReads = 0, 0, 0
			if _, err := a.observePRCandidate(ctx, p, clock); err == nil {
				t.Fatal("invalid remote snapshot accepted")
			}
		})
	}
	mode = "push"
	e, err := a.observePushEvidence(ctx, p.push, clock)
	if err != nil || !e.freshFor(p.push, now) {
		t.Fatal("valid push snapshot rejected", err)
	}
	if _, err := a.observePRCandidate(ctx, p, clock); err == nil {
		t.Fatal("push accepted as PR")
	}
	mode = ""
	if _, err := a.observePushEvidence(ctx, p.push, clock); err == nil {
		t.Fatal("PR accepted as push")
	}
	if _, err := a.observePRCandidate(ctx, p, nil); err == nil {
		t.Fatal("nil clock accepted")
	}
	if _, err := a.observePRRelationship(ctx, p, nil); err == nil {
		t.Fatal("nil relationship clock accepted")
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sign := func(statement githubExecutionStatement) githubExecutionProof {
		t.Helper()
		raw, err := json.Marshal(statement)
		if err != nil {
			t.Fatal(err)
		}
		return githubExecutionProof{statement.IssuedUnix, ed25519.Sign(private, raw)}
	}
	prEvidence := githubCIEvidence{candidate.policy.push, candidate.runID, candidate.attempt, candidate.jobIDs, candidate.observedAt}
	prProof := sign(githubExecutionMessage(p.push, prEvidence, digest, &p, candidate.relationship, now.Unix()))
	if err := verifyGitHubExecution(p.push, prEvidence, digest, &p, candidate.relationship, prProof, public, now); err != nil {
		t.Fatal("PR proof rejected", err)
	}
	mode = "push"
	pushProof := sign(githubExecutionMessage(p.push, e, digest, nil, githubPRRelationship{}, now.Unix()))
	if err := verifyGitHubExecution(p.push, e, digest, nil, githubPRRelationship{}, pushProof, public, now); err != nil {
		t.Fatal("push proof rejected", err)
	}
	if _, err := a.observePushEvidence(ctx, p.push, nil); err == nil {
		t.Fatal("nil push clock accepted")
	}

	t.Run("trusted signer observer", func(t *testing.T) {
		policy := githubIndependentPolicy{}
		policy.execution = p.push
		policy.sourceDigest = digest
		observe, err := newGitHubTrustedSignerObserver(a, policy, clock)
		if err != nil {
			t.Fatal(err)
		}
		mode = "push"
		ev, rel, err := observe(ctx)
		if err != nil || !ev.freshFor(p.push, now) || rel != (githubPRRelationship{}) {
			t.Fatal("trusted push observation failed", err)
		}
		ev.policy.required[0] = "changed"
		if _, _, err := observe(ctx); err != nil {
			t.Fatal("caller mutated observer policy", err)
		}
		mode = "jobs-error"
		if _, _, err := observe(ctx); err == nil {
			t.Fatal("failed push observation accepted")
		}
		policy.pr = &p
		prObserver, err := newGitHubTrustedSignerObserver(a, policy, clock)
		if err != nil {
			t.Fatal(err)
		}
		mode = ""
		ev, rel, err = prObserver(ctx)
		if err != nil || !ev.freshFor(p.push, now) || rel.mergeSHA != merge {
			t.Fatal("trusted PR observation failed", err)
		}
		mode = "source-changed"
		if _, _, err := prObserver(ctx); err == nil {
			t.Fatal("untrusted workflow source accepted")
		}
		mode = "pr-missing"
		if _, _, err := prObserver(ctx); err == nil {
			t.Fatal("missing PR accepted")
		}
		mode = ""
		other := p
		other.push.sha = strings.Repeat("d", 40)
		bad := policy
		bad.pr = &other
		if _, err := newGitHubTrustedSignerObserver(a, bad, clock); err == nil {
			t.Fatal("inconsistent PR policy accepted")
		}
		if _, err := newGitHubTrustedSignerObserver(nil, policy, clock); err == nil {
			t.Fatal("missing app accepted")
		}
		if _, err := newGitHubTrustedSignerObserver(a, policy, nil); err == nil {
			t.Fatal("missing clock accepted")
		}
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		if _, _, err := prObserver(cancelled); err == nil {
			t.Fatal("cancelled observation accepted")
		}
		bad = policy
		bad.pr = nil
		bad.sourceDigest = strings.Repeat("f", 64)
		observe, err = newGitHubTrustedSignerObserver(a, bad, clock)
		if err != nil {
			t.Fatal(err)
		}
		mode = "push"
		if _, _, err := observe(ctx); err == nil {
			t.Fatal("incorrect pinned workflow digest accepted")
		}
		ticks := 0
		expiring, err := newGitHubTrustedSignerObserver(a, policy, func() time.Time {
			ticks++
			if ticks > 3 {
				return now.Add(githubEvidenceLifetime)
			}
			return now
		})
		if err != nil {
			t.Fatal(err)
		}
		mode = ""
		if _, _, err := expiring(ctx); err == nil {
			t.Fatal("expired observation accepted")
		}
		mode = "push"
	})
	t.Run("registered execution", func(t *testing.T) {
		ownerDSN := os.Getenv("BROWSEAI_TEST_OWNER_DSN")
		readerDSN := os.Getenv("BROWSEAI_TEST_VERIFIER_READER_DSN")
		operatorDSN := os.Getenv("BROWSEAI_TEST_VERIFIER_OPERATOR_DSN")
		if ownerDSN == "" && readerDSN == "" && operatorDSN == "" {
			t.Skip("isolated PostgreSQL integration only")
		}
		if ownerDSN == "" || readerDSN == "" || operatorDSN == "" {
			t.Fatal("incomplete verifier database configuration")
		}
		open := func(dsn string) *sql.DB {
			t.Helper()
			db, err := sql.Open("postgres", dsn)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			return db
		}
		owner, operator, reader := open(ownerDSN), open(operatorDSN), open(readerDSN)
		b := p.push.binding
		if _, err := owner.ExecContext(ctx, "SELECT browseai.provision_github_binding($1,$2,$3,$4,12,34)", b.tenantID, b.installationID, b.repositoryID, b.fullName); err != nil {
			t.Fatal(err)
		}
		const verifierID = "00000000-0000-4000-8000-000000009951"
		const keyID = "00000000-0000-4000-8000-000000009952"
		const approvalID = "00000000-0000-4000-8000-000000009953"
		registration := githubVerifierRegistration{b, verifierID, keyID, public, now.Add(-time.Minute), now.Add(time.Hour), approvalID}
		for _, wrong := range []*sql.DB{nil, owner, reader} {
			if err := registerGitHubVerifier(ctx, wrong, registration); err == nil {
				t.Fatal("wrong registration role accepted")
			}
		}
		weak := registration
		weak.keyID = "00000000-0000-4000-8000-000000009961"
		weak.approvalID = "00000000-0000-4000-8000-000000009962"
		weak.publicKey = make([]byte, 32)
		weak.publicKey[0], weak.publicKey[31] = 1, 128
		if err := registerGitHubVerifier(ctx, operator, weak); err == nil {
			t.Fatal("noncanonical key registered by operator wrapper")
		}
		if err := registerGitHubVerifier(ctx, operator, registration); err != nil {
			t.Fatal(err)
		}
		// Simulate legacy operator SQL provisioning of a malformed point.
		// It may be stored but the controller must never resolve it.
		if _, err := operator.ExecContext(ctx, "SELECT browseai.register_github_verifier($1,$2,$3,$4,$5,$6,$7,$8,$9)",
			b.tenantID, b.installationID, b.repositoryID, verifierID, weak.keyID, weak.publicKey, weak.from, weak.until, weak.approvalID); err != nil {
			t.Fatal("legacy fixture registration failed", err)
		}
		s := githubVerifierStore{reader}
		if _, err := s.key(ctx, b, githubRegisteredProof{verifierID, weak.keyID, pushProof}); err == nil {
			t.Fatal("legacy invalid key resolved")
		}
		registeredPR := githubRegisteredProof{verifierID, keyID, prProof}
		registeredPush := githubRegisteredProof{verifierID, keyID, pushProof}
		mode = ""
		if err := s.verifyPRExecution(ctx, a, p, digest, registeredPR, clock); err != nil {
			t.Fatal("registered PR rejected", err)
		}
		mode = "source-changed"
		if err := s.verifyPRExecution(ctx, a, p, digest, registeredPR, clock); err == nil {
			t.Fatal("changed source accepted")
		}
		mode = "push"
		if err := s.verifyPushExecution(ctx, a, p.push, digest, registeredPush, clock); err != nil {
			t.Fatal("registered push rejected", err)
		}
		for _, bad := range []githubRegisteredProof{
			{},
			{verifierID, "00000000-0000-4000-8000-000000009999", pushProof},
			{"00000000-0000-4000-8000-000000009999", keyID, pushProof},
			{verifierID, keyID, githubExecutionProof{now.Unix(), make([]byte, ed25519.SignatureSize)}},
			{verifierID, keyID, githubExecutionProof{now.Add(-25 * time.Hour).Unix(), pushProof.signature}},
		} {
			if err := s.verifyPushExecution(ctx, a, p.push, digest, bad, clock); err == nil {
				t.Fatal("invalid registered proof accepted")
			}
		}
		for _, wrong := range []githubVerifierStore{{}, {owner}, {operator}} {
			if _, err := wrong.key(ctx, b, registeredPush); err == nil {
				t.Fatal("invalid role accepted")
			}
		}
		foreign := b
		foreign.tenantID = "00000000-0000-4000-8000-000000000002"
		if _, err := s.key(ctx, foreign, registeredPush); err == nil {
			t.Fatal("foreign key resolved")
		}
		if err := s.verifyObserved(ctx, p.push, e, digest, nil, githubPRRelationship{}, registeredPush, func() time.Time { return now.Add(githubEvidenceLifetime) }); err == nil {
			t.Fatal("stale evidence accepted")
		}
		task := githubEvidenceTask{"00000000-0000-4000-8000-000000009954", "00000000-0000-4000-8000-000000009955", 4}
		signTask := func(ev githubCIEvidence, policy *githubPRPolicy, rel githubPRRelationship) githubRegisteredProof {
			t.Helper()
			proof := githubRegisteredProof{verifierID, keyID, githubExecutionProof{issuedUnix: now.Unix()}}
			raw, err := json.Marshal(githubTaskExecutionMessage(task, p.push, ev, digest, policy, rel, proof))
			if err != nil {
				t.Fatal(err)
			}
			proof.proof.signature = ed25519.Sign(private, raw)
			return proof
		}
		taskPush := signTask(e, nil, githubPRRelationship{})
		taskPR := signTask(prEvidence, &p, candidate.relationship)
		verified, err := s.verifyTaskPush(ctx, a, task, p.push, digest, taskPush, clock)
		if err != nil || verified.digest != sha256.Sum256(append(append([]byte(nil), verified.statement...), verified.signature...)) ||
			!verified.expires.Equal(now.Add(githubEvidenceLifetime)) || !verified.issued.Equal(now) {
			t.Fatal("task push proof rejected", err)
		}
		mode = ""
		if _, err := s.verifyTaskPR(ctx, a, task, p, digest, taskPR, clock); err != nil {
			t.Fatal("task PR proof rejected", err)
		}
		mode = "source-changed"
		if _, err := s.verifyTaskPR(ctx, a, task, p, digest, taskPR, clock); err == nil {
			t.Fatal("changed workflow accepted")
		}
		mode = "push"
		for _, changed := range []githubEvidenceTask{
			{task.projectID, task.taskID, task.revision + 1},
			{task.taskID, task.taskID, task.revision},
			{task.projectID, task.projectID, task.revision},
			{task.projectID, task.taskID, -1},
		} {
			if _, err := s.verifyTaskPush(ctx, a, changed, p.push, digest, taskPush, clock); err == nil {
				t.Fatal("changed task scope accepted")
			}
		}
		if _, err := s.verifyTaskPush(ctx, a, task, p.push, digest, registeredPush, clock); err == nil {
			t.Fatal("legacy v1 accepted as task proof")
		}
		if _, err := s.verifyTaskPush(ctx, a, task, p.push, digest, taskPR, clock); err == nil {
			t.Fatal("PR proof accepted as push")
		}
		badProof := taskPush
		badProof.keyID = "00000000-0000-4000-8000-000000009999"
		if _, err := s.verifyTaskPush(ctx, a, task, p.push, digest, badProof, clock); err == nil {
			t.Fatal("unregistered signer accepted")
		}
		writerDSN := os.Getenv("BROWSEAI_TEST_EVIDENCE_WRITER_DSN")
		if writerDSN == "" {
			t.Fatal("missing evidence writer configuration")
		}
		writerDB := open(writerDSN)
		w := githubEvidenceWriter{writerDB, s}
		if _, err := owner.ExecContext(ctx, "INSERT INTO browseai.projects(id,tenant_id,name,repository_id,repository,code_branch) VALUES($1,$2,'proof fixture',$3,$4,$5)", task.projectID, b.tenantID, b.repositoryID, b.fullName, p.push.branch); err != nil {
			t.Fatal(err)
		}
		if _, err := owner.ExecContext(ctx, "INSERT INTO browseai.tasks(id,tenant_id,project_id,name,state,stage_count,code_sha,revision) VALUES($1,$2,$3,'proof fixture','WAIT_CI',1,$4,$5)", task.taskID, b.tenantID, task.projectID, p.push.sha, task.revision); err != nil {
			t.Fatal(err)
		}
		for _, db := range []*sql.DB{owner, operator, reader} {
			if _, err := (githubEvidenceWriter{db, s}).writePush(ctx, a, task, p.push, digest, taskPush, clock); err == nil {
				t.Fatal("wrong writer role accepted")
			}
		}
		if _, err := reader.ExecContext(ctx, "SELECT browseai.insert_github_ci_evidence($1,$2,$3)", verified.statement, verified.signature, verified.observed); err == nil {
			t.Fatal("reader could call writer")
		}
		if _, err := writerDB.ExecContext(ctx, "SELECT * FROM browseai.github_ci_evidence"); err == nil {
			t.Fatal("writer could read ledger")
		}
		if _, err := w.writePush(ctx, a, task, p.push, digest, registeredPush, clock); err == nil {
			t.Fatal("legacy proof persisted")
		}
		if _, err := w.writePush(ctx, a, task, p.push, digest, taskPush, clock); err != nil {
			t.Fatal("verified push persistence failed", err)
		}
		if _, err := w.writePush(ctx, a, task, p.push, digest, taskPush, clock); err == nil {
			t.Fatal("duplicate persistence accepted")
		}
		mode = ""
		if _, err := w.writePR(ctx, a, task, p, digest, taskPR, clock); err != nil {
			t.Fatal("verified PR persistence failed", err)
		}
		var count int
		if err := owner.QueryRowContext(ctx, "SELECT count(*) FROM browseai.github_ci_evidence WHERE task_id=$1 AND task_revision=$2 AND head_sha=$3 AND evidence_digest=sha256(statement||signature)", task.taskID, task.revision, p.push.sha).Scan(&count); err != nil || count != 2 {
			t.Fatal("unexpected persisted evidence", count, err)
		}
		testGitHubEvidenceConsumption(t, ctx, owner, writerDB, reader, operator, b.tenantID, task)
		if _, err := owner.ExecContext(ctx, "UPDATE browseai.tasks SET revision=revision+1 WHERE id=$1", task.taskID); err != nil {
			t.Fatal(err)
		}
		mode = "push"
		if _, err := w.insert(ctx, verified, now); err == nil {
			t.Fatal("stale task proof persisted")
		}
		if _, err := operator.ExecContext(ctx, "SELECT browseai.revoke_github_verifier($1,$2,$3,$4,gen_random_uuid())", b.tenantID, b.installationID, b.repositoryID, keyID); err != nil {
			t.Fatal(err)
		}
		if err := s.verifyPushExecution(ctx, a, p.push, digest, registeredPush, clock); err == nil {
			t.Fatal("revoked signer accepted")
		}
		if _, err := s.verifyTaskPush(ctx, a, task, p.push, digest, taskPush, clock); err == nil {
			t.Fatal("revoked task signer accepted")
		}
	})
}
