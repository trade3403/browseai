package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"strings"
	"time"
)

const githubWorkflowSourceLimit = 256 * 1024

type githubWorkflowSource struct {
	Type     string `json:"type"`
	Path     string `json:"path"`
	Size     int    `json:"size"`
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
	Target   string `json:"target"`
}

func githubWorkflowDigestValid(digest string) bool {
	raw, err := hex.DecodeString(digest)
	return err == nil && len(raw) == sha256.Size && digest == strings.ToLower(digest)
}

// The expected digest must be supplied by an independent trusted operator,
// not learned from the candidate commit or its Actions output.
func verifyGitHubWorkflowSource(path, expected string, source githubWorkflowSource) error {
	if !githubWorkflowDigestValid(expected) || source.Type != "file" || source.Path != path ||
		source.Target != "" || source.Encoding != "base64" || source.Size <= 0 || source.Size > githubWorkflowSourceLimit {
		return errGitHubAuthorization
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(source.Content)
	if err != nil || len(raw) != source.Size {
		return errGitHubAuthorization
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != expected {
		return errGitHubAuthorization
	}
	return nil
}

// Fetch exact immutable revisions, never a mutable branch or an upstream
// download URL. For PRs require the approved bytes at head, base and merge.
// This does not establish what code the runner actually checked out.
func (a *githubApp) verifyWorkflowSource(ctx context.Context, p githubEvidencePolicy, expected string, revisions []string, now time.Time) error {
	if !p.valid() || !githubWorkflowDigestValid(expected) || len(revisions) == 0 || len(revisions) > 3 {
		return errGitHubAuthorization
	}
	seen := make(map[string]bool)
	for _, revision := range revisions {
		if !taskSHA.MatchString(revision) || seen[revision] {
			return errGitHubAuthorization
		}
		seen[revision] = true
	}
	if !seen[p.sha] {
		return errGitHubAuthorization
	}
	token, err := a.mint(ctx, p.binding, now)
	if err != nil {
		return err
	}
	parts := strings.Split(p.binding.fullName, "/")
	path := "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
	var repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		Archived bool   `json:"archived"`
		Disabled bool   `json:"disabled"`
	}
	if err := a.readJSON(ctx, token, path, nil, now, &repository); err != nil {
		return err
	}
	if repository.ID != p.binding.repositoryID || !strings.EqualFold(repository.FullName, p.binding.fullName) || repository.Archived || repository.Disabled {
		return errGitHubAuthorization
	}
	file := strings.TrimPrefix(p.path, ".github/workflows/")
	endpoint := path + "/contents/.github/workflows/" + url.PathEscape(file)
	for _, revision := range revisions {
		var source githubWorkflowSource
		if err := a.readJSON(ctx, token, endpoint, url.Values{"ref": {revision}}, now, &source); err != nil {
			return err
		}
		if err := verifyGitHubWorkflowSource(p.path, expected, source); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (a *githubApp) verifyPushWorkflowSource(ctx context.Context, p githubEvidencePolicy, expected string, now time.Time) error {
	return a.verifyWorkflowSource(ctx, p, expected, []string{p.sha}, now)
}

func (a *githubApp) verifyPRWorkflowSource(ctx context.Context, p githubPRPolicy, relationship githubPRRelationship, expected string, now time.Time) error {
	if !p.valid() || relationship.number != p.number || relationship.headSHA != p.push.sha || relationship.baseSHA != p.baseSHA {
		return errGitHubAuthorization
	}
	return a.verifyWorkflowSource(ctx, p.push, expected, []string{p.push.sha, p.baseSHA, relationship.mergeSHA}, now)
}
