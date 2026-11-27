package main

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type githubWorkflow struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"`
}

type githubRun struct {
	ID         int64  `json:"id"`
	WorkflowID int64  `json:"workflow_id"`
	Attempt    int64  `json:"run_attempt"`
	HeadSHA    string `json:"head_sha"`
	HeadBranch string `json:"head_branch"`
	Event      string `json:"event"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Repository struct {
		ID int64 `json:"id"`
	} `json:"repository"`
	HeadRepository struct {
		ID int64 `json:"id"`
	} `json:"head_repository"`
}

// Bounded pagination. Reject partial or unstable lists rather than returning
// a deceptively complete observation. Never follow an upstream Link URL.
const githubActionsPageSize = 30
const githubActionsMaxPages = 10

func githubActionsPages[T any](ctx context.Context, a *githubApp, token githubToken, path string, filters url.Values, key string, now time.Time, identity func(T) int64) ([]T, error) {
	items := make([]T, 0)
	seen := make(map[int64]bool)
	total := -1
	for page := 1; page <= githubActionsMaxPages; page++ {
		q := make(url.Values)
		for k, values := range filters {
			q[k] = append([]string(nil), values...)
		}
		q.Set("page", strconv.Itoa(page))
		q.Set("per_page", strconv.Itoa(githubActionsPageSize))
		var response struct {
			Total     *int `json:"total_count"`
			Workflows []T  `json:"workflows"`
			Runs      []T  `json:"workflow_runs"`
			Jobs      []T  `json:"jobs"`
		}
		if err := a.readJSON(ctx, token, path, q, now, &response); err != nil {
			return nil, err
		}
		var batch []T
		switch key {
		case "workflows":
			batch = response.Workflows
		case "workflow_runs":
			batch = response.Runs
		case "jobs":
			batch = response.Jobs
		default:
			return nil, errGitHubUnavailable
		}
		if response.Total == nil || *response.Total < 0 || *response.Total > githubActionsMaxPages*githubActionsPageSize || batch == nil || len(batch) > githubActionsPageSize {
			return nil, errGitHubUnavailable
		}
		if total < 0 {
			total = *response.Total
		}
		if total != *response.Total {
			return nil, errGitHubUnavailable
		}
		for _, item := range batch {
			id := identity(item)
			if id <= 0 || seen[id] {
				return nil, errGitHubUnavailable
			}
			seen[id] = true
			items = append(items, item)
		}
		if len(items) > total {
			return nil, errGitHubUnavailable
		}
		if len(items) == total {
			return items, nil
		}
		if len(batch) != githubActionsPageSize {
			return nil, errGitHubUnavailable
		}
	}
	return nil, errGitHubUnavailable
}

// Workflow and run listings are observations only. Their status and
// conclusion cannot authorize a task transition without job evidence.
func (a *githubApp) listActions(ctx context.Context, binding githubBinding, branch, sha string, now time.Time) ([]githubWorkflow, []githubRun, error) {
	if !(projectCreate{Name: "verify", RepositoryID: binding.repositoryID, Repository: binding.fullName, CodeBranch: branch}).valid() || !taskSHA.MatchString(sha) {
		return nil, nil, errGitHubAuthorization
	}
	token, err := a.mint(ctx, binding, now)
	if err != nil {
		return nil, nil, err
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
		return nil, nil, err
	}
	if repository.ID != binding.repositoryID || !strings.EqualFold(repository.FullName, binding.fullName) || repository.Archived || repository.Disabled {
		return nil, nil, errGitHubAuthorization
	}
	workflows, err := githubActionsPages(ctx, a, token, path+"/actions/workflows", nil, "workflows", now, func(w githubWorkflow) int64 { return w.ID })
	if err != nil {
		return nil, nil, err
	}
	runs, err := githubActionsPages(ctx, a, token, path+"/actions/runs", url.Values{"branch": {branch}, "head_sha": {sha}}, "workflow_runs", now, func(r githubRun) int64 { return r.ID })
	if err != nil {
		return nil, nil, err
	}
	for _, run := range runs {
		if run.Repository.ID != binding.repositoryID || run.HeadRepository.ID != binding.repositoryID || run.HeadBranch != branch || run.HeadSHA != sha || run.WorkflowID <= 0 || run.Attempt <= 0 {
			return nil, nil, errGitHubAuthorization
		}
	}
	return workflows, runs, nil
}
