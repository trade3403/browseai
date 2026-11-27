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
		if _, err := operator.ExecContext(ctx, "SELECT browseai.register_github_verifier($1,$2,$3,$4,$5,$6,$7,$8,$9)",
			b.tenantID, b.installationID, b.repositoryID, verifierID, keyID, []byte(public), now.Add(-time.Minute), now.Add(time.Hour), approvalID); err != nil {
			t.Fatal(err)
		}
		s := githubVerifierStore{reader}
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
		if _, err := operator.ExecContext(ctx, "SELECT browseai.revoke_github_verifier($1,$2,$3,$4,gen_random_uuid())", b.tenantID, b.installationID, b.repositoryID, keyID); err != nil {
			t.Fatal(err)
		}
		if err := s.verifyPushExecution(ctx, a, p.push, digest, registeredPush, clock); err == nil {
			t.Fatal("revoked signer accepted")
		}
	})
}
