package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type githubJob struct {
	ID         int64  `json:"id"`
	RunID      int64  `json:"run_id"`
	RunAttempt int64  `json:"run_attempt"`
	HeadSHA    string `json:"head_sha"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

type githubCIState string

const (
	githubCIUnknown githubCIState = "unknown"
	githubCIPending githubCIState = "pending"
	githubCIFailed  githubCIState = "failed"
	githubCICancel  githubCIState = "cancelled"
	githubCIPassed  githubCIState = "passed"
)

// Success is only a candidate pending authoritative workflow and PR evidence.
func classifyGitHubCI(run githubRun, jobs []githubJob, required []string) githubCIState {
	if run.ID <= 0 || run.Attempt <= 0 || !taskSHA.MatchString(run.HeadSHA) {
		return githubCIUnknown
	}
	switch run.Status {
	case "queued", "in_progress", "requested", "waiting", "pending":
		if run.Conclusion == "" {
			return githubCIPending
		}
		return githubCIUnknown
	case "completed":
	default:
		return githubCIUnknown
	}
	switch run.Conclusion {
	case "failure", "timed_out", "action_required", "startup_failure", "stale":
		return githubCIFailed
	case "cancelled":
		return githubCICancel
	case "success":
	default:
		return githubCIUnknown
	}
	if len(required) == 0 || len(jobs) == 0 {
		return githubCIUnknown
	}
	seen := make(map[string]bool)
	ids := make(map[int64]bool)
	for _, job := range jobs {
		if job.ID <= 0 || ids[job.ID] || job.RunID != run.ID || job.RunAttempt != run.Attempt || job.HeadSHA != run.HeadSHA || job.Name == "" || seen[job.Name] {
			return githubCIUnknown
		}
		ids[job.ID] = true
		seen[job.Name] = true
		if job.Status != "completed" || job.Conclusion != "success" {
			return githubCIUnknown
		}
	}
	expected := make(map[string]bool)
	for _, name := range required {
		if name == "" || expected[name] || !seen[name] {
			return githubCIUnknown
		}
		expected[name] = true
	}
	return githubCIPassed
}

// Recheck the current run around the exact-attempt jobs read.
func (a *githubApp) listAttemptJobs(ctx context.Context, binding githubBinding, run githubRun, now time.Time) ([]githubJob, error) {
	if run.ID <= 0 || run.WorkflowID <= 0 || run.Attempt <= 0 || run.Repository.ID != binding.repositoryID || run.HeadRepository.ID != binding.repositoryID ||
		!(projectCreate{Name: "verify", RepositoryID: binding.repositoryID, Repository: binding.fullName, CodeBranch: run.HeadBranch}).valid() || !taskSHA.MatchString(run.HeadSHA) {
		return nil, errGitHubAuthorization
	}
	token, err := a.mint(ctx, binding, now)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(binding.fullName, "/")
	path := "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
	endpoint := fmt.Sprintf("%s/actions/runs/%d", path, run.ID)
	check := func() error {
		var current githubRun
		if err := a.readJSON(ctx, token, endpoint, nil, now, &current); err != nil {
			return err
		}
		if current != run {
			return errGitHubUnavailable
		}
		return nil
	}
	if err := check(); err != nil {
		return nil, err
	}
	jobs, err := githubActionsPages(ctx, a, token, fmt.Sprintf("%s/attempts/%d/jobs", endpoint, run.Attempt), nil, "jobs", now, func(j githubJob) int64 { return j.ID })
	if err != nil {
		return nil, err
	}
	for _, job := range jobs {
		if job.RunID != run.ID || job.RunAttempt != run.Attempt || job.HeadSHA != run.HeadSHA {
			return nil, errGitHubAuthorization
		}
	}
	if err := check(); err != nil {
		return nil, err
	}
	return jobs, nil
}
