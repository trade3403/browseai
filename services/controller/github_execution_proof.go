package main

import (
	"crypto/ed25519"
	"encoding/json"
	"sort"
	"time"
)

// Execution statements are signed by an independently trusted verifier,
// never by a repository-controlled job. They assert checkout and test
// execution for every listed job, including trusted test dependencies.
type githubExecutionStatement struct {
	Domain       string   `json:"domain"`
	Tenant       string   `json:"tenant"`
	Installation int64    `json:"installation"`
	Repository   int64    `json:"repository"`
	FullName     string   `json:"full_name"`
	Branch       string   `json:"branch"`
	HeadSHA      string   `json:"head_sha"`
	BaseBranch   string   `json:"base_branch"`
	BaseSHA      string   `json:"base_sha"`
	PR           int64    `json:"pr"`
	ExecutedSHA  string   `json:"executed_sha"`
	Event        string   `json:"event"`
	Workflow     int64    `json:"workflow"`
	Name         string   `json:"name"`
	Path         string   `json:"path"`
	SourceDigest string   `json:"source_digest"`
	Run          int64    `json:"run"`
	Attempt      int64    `json:"attempt"`
	Jobs         []int64  `json:"jobs"`
	Required     []string `json:"required"`
	IssuedUnix   int64    `json:"issued_unix"`
}

const githubExecutionDomain = "browseai-ci-execution-v1"
const githubExecutionMaxAge = 24 * time.Hour

type githubExecutionProof struct {
	issuedUnix int64
	signature  []byte
}

func githubExecutionMessage(p githubEvidencePolicy, e githubCIEvidence, digest string, pr *githubPRPolicy, rel githubPRRelationship, issued int64) githubExecutionStatement {
	s := githubExecutionStatement{
		Domain: githubExecutionDomain, Tenant: p.binding.tenantID, Installation: p.binding.installationID,
		Repository: p.binding.repositoryID, FullName: p.binding.fullName, Branch: p.branch, HeadSHA: p.sha,
		ExecutedSHA: p.sha, Event: "push", Workflow: p.workflowID, Name: p.name, Path: p.path,
		SourceDigest: digest, Run: e.runID, Attempt: e.attempt, Jobs: append([]int64(nil), e.jobIDs...),
		Required: append([]string(nil), p.required...), IssuedUnix: issued,
	}
	if pr != nil {
		s.Event, s.PR, s.BaseBranch, s.BaseSHA, s.ExecutedSHA = "pull_request", pr.number, pr.baseBranch, pr.baseSHA, rel.mergeSHA
	}
	sort.Strings(s.Required)
	sort.Slice(s.Jobs, func(i, j int) bool { return s.Jobs[i] < s.Jobs[j] })
	return s
}

func validateGitHubExecution(p githubEvidencePolicy, e githubCIEvidence, digest string, pr *githubPRPolicy, rel githubPRRelationship, proof githubExecutionProof, key ed25519.PublicKey, now time.Time) error {
	if !e.freshFor(p, now) || !githubWorkflowDigestValid(digest) ||
		!githubStrictEd25519Key(key) || len(proof.signature) != ed25519.SignatureSize || proof.issuedUnix <= 0 {
		return errGitHubAuthorization
	}
	issued := time.Unix(proof.issuedUnix, 0)
	if issued.After(now) || now.Sub(issued) >= githubExecutionMaxAge {
		return errGitHubAuthorization
	}
	if pr != nil && (!pr.valid() || !e.freshFor(pr.push, now) ||
		rel.number != pr.number || rel.headSHA != p.sha || rel.baseSHA != pr.baseSHA ||
		!taskSHA.MatchString(rel.mergeSHA) || rel.mergeSHA == rel.headSHA || rel.mergeSHA == rel.baseSHA) {
		return errGitHubAuthorization
	}
	for i, id := range e.jobIDs {
		if id <= 0 || (i > 0 && id <= e.jobIDs[i-1]) {
			return errGitHubAuthorization
		}
	}
	return nil
}

// Legacy v1 verifies execution only. It must never authorize ledger writes.
func verifyGitHubExecution(p githubEvidencePolicy, e githubCIEvidence, digest string, pr *githubPRPolicy, rel githubPRRelationship, proof githubExecutionProof, key ed25519.PublicKey, now time.Time) error {
	if err := validateGitHubExecution(p, e, digest, pr, rel, proof, key, now); err != nil {
		return err
	}
	raw, err := json.Marshal(githubExecutionMessage(p, e, digest, pr, rel, proof.issuedUnix))
	if err != nil || !ed25519.Verify(key, raw, proof.signature) {
		return errGitHubAuthorization
	}
	return nil
}
