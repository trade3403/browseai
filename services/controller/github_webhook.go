package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
)

const githubWebhookMaxBody = 2 * 1024 * 1024

var githubDeliveryID = regexp.MustCompile("^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$")

// A verified delivery is only a hint to re-fetch authoritative GitHub state.
// Headers are not signed by GitHub; never use them as CI success evidence.
type githubWebhookDelivery struct {
	deliveryID     string
	event          string
	installationID int64
	repositoryID   int64
	bodyDigest     [32]byte
}

func verifyGitHubWebhook(r *http.Request, secret []byte) (githubWebhookDelivery, error) {
	var d githubWebhookDelivery
	if r.Method != http.MethodPost || len(secret) < 32 || len(secret) > 4096 || r.Body == nil || r.ContentLength > githubWebhookMaxBody {
		return d, errGitHubAuthorization
	}
	signatures := r.Header.Values("X-Hub-Signature-256")
	deliveries := r.Header.Values("X-GitHub-Delivery")
	events := r.Header.Values("X-GitHub-Event")
	if len(signatures) != 1 || len(deliveries) != 1 || len(events) != 1 || !githubDeliveryID.MatchString(deliveries[0]) {
		return d, errGitHubAuthorization
	}
	switch events[0] {
	case "workflow_run", "check_run", "check_suite", "push", "pull_request":
	default:
		return d, errGitHubAuthorization
	}
	sig := signatures[0]
	if len(sig) != 71 || !strings.HasPrefix(sig, "sha256=") {
		return d, errGitHubAuthorization
	}
	expected, err := hex.DecodeString(sig[7:])
	if err != nil {
		return d, errGitHubAuthorization
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, githubWebhookMaxBody+1))
	if err != nil || len(raw) > githubWebhookMaxBody {
		return d, errGitHubAuthorization
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(raw)
	if !hmac.Equal(mac.Sum(nil), expected) {
		return d, errGitHubAuthorization
	}
	var payload struct {
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
		Repository struct {
			ID int64 `json:"id"`
		} `json:"repository"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Installation.ID <= 0 || payload.Repository.ID <= 0 {
		return d, errGitHubAuthorization
	}
	return githubWebhookDelivery{
		deliveryID:     strings.ToLower(deliveries[0]),
		event:          events[0],
		installationID: payload.Installation.ID,
		repositoryID:   payload.Repository.ID,
		bodyDigest:     sha256.Sum256(raw),
	}, nil
}
