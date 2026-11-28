package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGitHubFailureConfig(t *testing.T) {
	c := githubFailureConfig{
		Domain: githubFailureConfigDomain, Grant: "00000000-0000-4000-8000-000000009976",
		Tenant: "00000000-0000-4000-8000-000000000001", Task: "00000000-0000-4000-8000-000000000003",
		Project: "00000000-0000-4000-8000-000000000002", Revision: 4, Installation: 12, Repository: 34,
		FullName: "owner/repo", Branch: "feat/demo", HeadSHA: strings.Repeat("a", 40), Workflow: 56,
		Name: "CI", Path: ".github/workflows/ci.yml", Required: []string{"gate"}, Source: strings.Repeat("b", 64),
		AppIssuer: "12", Expires: "2026-11-28T12:00:00Z",
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeGitHubFailureConfig(raw); err != nil {
		t.Fatal("valid config rejected", err)
	}
	for _, bad := range [][]byte{append(append([]byte(nil), raw...), '\n'), []byte("{}"), []byte(strings.Replace(string(raw), "\"revision\":4", "\"revision\":-1", 1))} {
		if _, err := decodeGitHubFailureConfig(bad); err == nil {
			t.Fatal("invalid config accepted")
		}
	}
	sum := sha256.Sum256(raw)
	line := "APPROVE issue " + hex.EncodeToString(sum[:]) + "\n"
	if err := githubFailureGrantApproval(raw, "issue", strings.NewReader(line)); err != nil {
		t.Fatal("valid approval rejected")
	}
	if err := githubFailureGrantApproval(raw, "revoke", strings.NewReader(line)); err == nil {
		t.Fatal("approval action substitution")
	}
	if err := githubFailureGrantApproval(raw, "issue", strings.NewReader(line+"extra")); err == nil {
		t.Fatal("trailing approval accepted")
	}
}

func testGitHubFailureGrantOperator(t *testing.T, ctx context.Context, owner *sql.DB, candidate githubCIFailureCandidate) string {
	t.Helper()
	dsn := os.Getenv("BROWSEAI_TEST_FAILURE_GRANT_DSN")
	if dsn == "" {
		t.Fatal("missing grant operator fixture")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	p := candidate.policy
	c := githubFailureConfig{
		Domain: githubFailureConfigDomain, Grant: "00000000-0000-4000-8000-000000009976",
		Tenant: p.binding.tenantID, Task: candidate.task.taskID, Project: candidate.task.projectID, Revision: candidate.task.revision,
		Installation: p.binding.installationID, Repository: p.binding.repositoryID, FullName: p.binding.fullName, Branch: p.branch,
		HeadSHA: p.sha, Workflow: p.workflowID, Name: p.name, Path: p.path, Required: p.required, Source: candidate.source,
		AppIssuer: "12", Expires: time.Now().UTC().Add(5 * time.Minute).Truncate(time.Second).Format(time.RFC3339),
	}
	if candidate.event != "push" {
		t.Fatal("expected push fixture")
	}
	if err := githubFailureGrantReady(ctx, db); err != nil {
		t.Fatal("operator role rejected", err)
	}
	if err := githubFailureGrantReady(ctx, owner); err == nil {
		t.Fatal("owner accepted")
	}
	invalid := c
	invalid.Revision++
	if err := changeGitHubFailureGrant(ctx, db, invalid, "issue"); err == nil {
		t.Fatal("stale revision granted")
	}
	invalid = c
	invalid.Expires = time.Now().UTC().Add(-time.Minute).Truncate(time.Second).Format(time.RFC3339)
	if err := changeGitHubFailureGrant(ctx, db, invalid, "issue"); err == nil {
		t.Fatal("expired grant issued")
	}
	revoked := c
	revoked.Grant = "00000000-0000-4000-8000-000000009977"
	revoked.Workflow++
	if err := changeGitHubFailureGrant(ctx, db, revoked, "issue"); err != nil {
		t.Fatal("revocation fixture issue failed", err)
	}
	if err := changeGitHubFailureGrant(ctx, db, revoked, "revoke"); err != nil {
		t.Fatal("revocation failed", err)
	}
	if err := changeGitHubFailureGrant(ctx, db, revoked, "revoke"); err == nil {
		t.Fatal("duplicate revoke accepted")
	}
	var audit bool
	if err := owner.QueryRowContext(ctx, "SELECT approved_by='browseai_failure_grant_operator' AND revoked_by=approved_by AND revoked_at IS NOT NULL FROM browseai.github_failure_observer_grants WHERE id=$1", revoked.Grant).Scan(&audit); err != nil || !audit {
		t.Fatal("missing revocation audit", audit, err)
	}
	if err := changeGitHubFailureGrant(ctx, db, c, "issue"); err != nil {
		t.Fatal("grant issue failed", err)
	}
	if err := changeGitHubFailureGrant(ctx, db, c, "issue"); err == nil {
		t.Fatal("duplicate grant accepted")
	}
	return c.Grant
}
