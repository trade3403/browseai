package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var errGitHubUnavailable = errors.New("GitHub read unavailable")

// The caller schedules another observation no earlier than retryAt. Never
// busy-loop on a rate limit, and never interpret an unavailable read as CI pass.
type githubReadError struct {
	retryAt time.Time
}

func (e githubReadError) Error() string { return "GitHub read temporarily unavailable" }

func githubRetryAt(h http.Header, now time.Time) time.Time {
	retry := now.Add(time.Minute)
	if seconds, err := strconv.ParseInt(h.Get("Retry-After"), 10, 32); err == nil && seconds >= 0 {
		if next := now.Add(time.Duration(seconds) * time.Second); next.After(retry) {
			retry = next
		}
	} else if next, err := http.ParseTime(h.Get("Retry-After")); err == nil && next.After(retry) {
		retry = next
	}
	if h.Get("X-RateLimit-Remaining") == "0" {
		if unix, err := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			if next := time.Unix(unix, 0).Add(time.Second); next.After(retry) {
				retry = next
			}
		}
	}
	return retry
}

// Only internally constructed relative paths are accepted. No links supplied
// by GitHub or by a user are followed with credentials. Pagination callers
// must construct the next page locally.
func (a *githubApp) readJSON(ctx context.Context, token githubToken, path string, query url.Values, now time.Time, dst any) error {
	if token.value == "" || !token.expiresAt.After(now.Add(30*time.Second)) ||
		!strings.HasPrefix(path, "/repos/") || strings.ContainsAny(path, "?#\\") {
		return errGitHubAuthorization
	}
	endpoint := "https://api.github.com" + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil || req.URL.Host != "api.github.com" || req.URL.Scheme != "https" {
		return errGitHubAuthorization
	}
	req.Header.Set("Authorization", "Bearer "+token.value)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	res, err := a.client.Do(req)
	if err != nil {
		return errGitHubUnavailable
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusTooManyRequests:
		return githubReadError{retryAt: githubRetryAt(res.Header, now)}
	case http.StatusForbidden:
		if res.Header.Get("Retry-After") != "" || res.Header.Get("X-RateLimit-Remaining") == "0" {
			return githubReadError{retryAt: githubRetryAt(res.Header, now)}
		}
		return errGitHubAuthorization
	case http.StatusUnauthorized, http.StatusNotFound:
		return errGitHubAuthorization
	default:
		if res.StatusCode >= 500 {
			return githubReadError{retryAt: githubRetryAt(res.Header, now)}
		}
		return errGitHubUnavailable
	}
	const limit = 2 * 1024 * 1024
	raw, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil || len(raw) > limit || json.Unmarshal(raw, dst) != nil {
		return errGitHubUnavailable
	}
	return nil
}

type githubBranchObservation struct {
	target  verifiedBranch
	headSHA string
}

// Each observation starts with a fresh, narrowly scoped installation token.
// A remote repository and branch mismatch cannot produce verifiedBranch.
func (a *githubApp) observeBranch(ctx context.Context, binding githubBinding, branch string, now time.Time) (githubBranchObservation, error) {
	if !((projectCreate{Name: "verify", RepositoryID: binding.repositoryID, Repository: binding.fullName, CodeBranch: branch}).valid()) {
		return githubBranchObservation{}, errGitHubAuthorization
	}
	token, err := a.mint(ctx, binding, now)
	if err != nil {
		return githubBranchObservation{}, err
	}
	parts := strings.Split(binding.fullName, "/")
	path := "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
	var repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		Archived bool   `json:"archived"`
		Disabled bool   `json:"disabled"`
	}
	if err := a.readJSON(ctx, token, path, nil, now, &repository); err != nil {
		return githubBranchObservation{}, err
	}
	if repository.ID != binding.repositoryID || !strings.EqualFold(repository.FullName, binding.fullName) || repository.Archived || repository.Disabled {
		return githubBranchObservation{}, errGitHubAuthorization
	}
	var ref struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := a.readJSON(ctx, token, path+"/branches/"+url.PathEscape(branch), nil, now, &ref); err != nil {
		return githubBranchObservation{}, err
	}
	if ref.Name != branch || !taskSHA.MatchString(ref.Commit.SHA) {
		return githubBranchObservation{}, errGitHubAuthorization
	}
	return githubBranchObservation{target: verifiedBranch{repositoryID: repository.ID, branch: ref.Name}, headSHA: ref.Commit.SHA}, nil
}
