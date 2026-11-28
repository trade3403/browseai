package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

const githubSandboxBuildDomain = "browseai-sandbox-build-v1"
const githubSandboxBuildMaxAge = 24 * time.Hour

// These pins come from protected operator configuration, not from the
// repository, build receipt or execution request.
type githubSandboxBuildPolicy struct {
	builderID        string
	builderKey       ed25519.PublicKey
	baseImageDigest  string
	dependencyDigest string
	sourceTreeDigest string
}

type githubSandboxBuildReceipt struct {
	Domain           string            `json:"domain"`
	Builder          string            `json:"builder"`
	RequestDigest    string            `json:"request_digest"`
	CheckoutSHA      string            `json:"checkout_sha"`
	SourceTreeDigest string            `json:"source_tree_digest"`
	HarnessDigest    string            `json:"harness_digest"`
	BaseImageDigest  string            `json:"base_image_digest"`
	DependencyDigest string            `json:"dependency_digest"`
	ImageDigest      string            `json:"image_digest"`
	Jobs             []githubRemoteJob `json:"jobs"`
	BuiltUnix        int64             `json:"built_unix"`
}

type githubSandboxBuildProof struct {
	receipt   githubSandboxBuildReceipt
	signature []byte
}

// Verify independently authenticated build provenance. The builder must
// actually assemble and audit the offline image outside repository control.
// A signed claim is not a substitute for trustworthy builder deployment.
func verifyGitHubSandboxBuild(plan githubSandboxPlan, policy githubSandboxBuildPolicy, proof githubSandboxBuildProof, now time.Time) error {
	r := proof.receipt
	if !resourceUUID.MatchString(policy.builderID) || !githubStrictEd25519Key(policy.builderKey) ||
		len(proof.signature) != ed25519.SignatureSize ||
		!githubWorkflowDigestValid(policy.baseImageDigest) || !githubWorkflowDigestValid(policy.dependencyDigest) ||
		!githubWorkflowDigestValid(policy.sourceTreeDigest) ||
		r.Domain != githubSandboxBuildDomain || r.Builder != policy.builderID ||
		r.BaseImageDigest != policy.baseImageDigest || r.DependencyDigest != policy.dependencyDigest ||
		r.SourceTreeDigest != policy.sourceTreeDigest ||
		r.CheckoutSHA != plan.request.statement.Execution.ExecutedSHA ||
		r.HarnessDigest != plan.request.harnessDigest || r.ImageDigest != plan.request.imageDigest ||
		r.BuiltUnix <= 0 || len(r.Jobs) != len(plan.jobs) {
		return errGitHubAuthorization
	}
	built := time.Unix(r.BuiltUnix, 0)
	if built.After(now) || now.Sub(built) >= githubSandboxBuildMaxAge {
		return errGitHubAuthorization
	}
	request, err := githubSandboxRequestBytes(plan.request)
	if err != nil {
		return errGitHubAuthorization
	}
	sum := sha256.Sum256(request)
	if r.RequestDigest != hex.EncodeToString(sum[:]) {
		return errGitHubAuthorization
	}
	for i, j := range plan.jobs {
		if !j.passed || r.Jobs[i] != (githubRemoteJob{j.id, j.name, true}) {
			return errGitHubAuthorization
		}
	}
	raw, err := json.Marshal(r)
	if err != nil || len(raw) > githubRemoteBodyLimit || !ed25519.Verify(policy.builderKey, raw, proof.signature) {
		return errGitHubAuthorization
	}
	return nil
}
