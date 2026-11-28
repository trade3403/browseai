package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

const githubExecutorReceiptDomain = "browseai-isolated-executor-receipt-v1"
const githubExecutorReceiptMaxAge = 5 * time.Minute

// The policy is independently provisioned by an operator, never taken from
// repository code, CI artifacts, a user request or the receipt itself.
type githubIndependentPolicy struct {
	task          githubEvidenceTask
	execution     githubEvidencePolicy
	pr            *githubPRPolicy
	sourceDigest  string
	harnessDigest string
	imageDigest   string
	verifierID    string
	keyID         string
}

// A trusted supervisor must attest the actual checkout and successful tests.
// Repository-controlled processes MUST NOT possess the supervisor key.
type githubExecutorReceipt struct {
	Domain          string `json:"domain"`
	StatementDigest string `json:"statement_digest"`
	CheckoutSHA     string `json:"checkout_sha"`
	HarnessDigest   string `json:"harness_digest"`
	ImageDigest     string `json:"image_digest"`
	StartedUnix     int64  `json:"started_unix"`
	FinishedUnix    int64  `json:"finished_unix"`
	AllPassed       bool   `json:"all_passed"`
}

type githubIndependentSigner struct {
	privateKey    ed25519.PrivateKey
	supervisorKey ed25519.PublicKey
}

// sign is an offline decision primitive, not a sandbox or an HTTP signing
// endpoint. Deploy it only outside the test executor's trust boundary.
// The caller must obtain policy from trusted configuration and fresh
// observations from GitHub, not from the repository or the receipt.
func (s githubIndependentSigner) sign(policy githubIndependentPolicy, e githubCIEvidence, rel githubPRRelationship, receipt githubExecutorReceipt, supervisorSignature []byte, now time.Time) (githubRegisteredProof, error) {
	if len(s.privateKey) != ed25519.PrivateKeySize || !githubStrictEd25519Key(s.supervisorKey) ||
		len(supervisorSignature) != ed25519.SignatureSize ||
		!policy.task.valid() || !resourceUUID.MatchString(policy.verifierID) || !resourceUUID.MatchString(policy.keyID) ||
		!githubWorkflowDigestValid(policy.sourceDigest) || !githubWorkflowDigestValid(policy.harnessDigest) ||
		!githubWorkflowDigestValid(policy.imageDigest) {
		return githubRegisteredProof{}, errGitHubAuthorization
	}
	// Reject a malformed private key or reuse of the signing identity as
	// the execution supervisor identity.
	public := s.privateKey.Public().(ed25519.PublicKey)
	if !bytes.Equal(s.privateKey, ed25519.NewKeyFromSeed(s.privateKey.Seed())) || bytes.Equal(public, s.supervisorKey) {
		return githubRegisteredProof{}, errGitHubAuthorization
	}
	if receipt.Domain != githubExecutorReceiptDomain || !receipt.AllPassed ||
		receipt.HarnessDigest != policy.harnessDigest || receipt.ImageDigest != policy.imageDigest ||
		receipt.StartedUnix <= 0 || receipt.FinishedUnix < receipt.StartedUnix {
		return githubRegisteredProof{}, errGitHubAuthorization
	}
	finished := time.Unix(receipt.FinishedUnix, 0)
	if finished.After(now) || now.Sub(finished) >= githubExecutorReceiptMaxAge {
		return githubRegisteredProof{}, errGitHubAuthorization
	}
	proof := githubRegisteredProof{
		verifierID: policy.verifierID, keyID: policy.keyID,
		proof: githubExecutionProof{issuedUnix: receipt.FinishedUnix, signature: make([]byte, ed25519.SignatureSize)},
	}
	if err := validateGitHubExecution(policy.execution, e, policy.sourceDigest, policy.pr, rel, proof.proof, public, now); err != nil {
		return githubRegisteredProof{}, err
	}
	statement := githubTaskExecutionMessage(policy.task, policy.execution, e, policy.sourceDigest, policy.pr, rel, proof)
	raw, err := json.Marshal(statement)
	if err != nil || len(raw) > 131072 {
		return githubRegisteredProof{}, errGitHubAuthorization
	}
	sum := sha256.Sum256(raw)
	if receipt.StatementDigest != hex.EncodeToString(sum[:]) || receipt.CheckoutSHA != statement.Execution.ExecutedSHA {
		return githubRegisteredProof{}, errGitHubAuthorization
	}
	receiptBytes, err := json.Marshal(receipt)
	if err != nil || !ed25519.Verify(s.supervisorKey, receiptBytes, supervisorSignature) {
		return githubRegisteredProof{}, errGitHubAuthorization
	}
	proof.proof.signature = ed25519.Sign(s.privateKey, raw)
	return proof, nil
}
