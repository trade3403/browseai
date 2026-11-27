package main

import (
	"context"
	"sort"
	"strings"
	"time"
)

// Evidence is a read-only snapshot, not a task transition or an attestation
// that the workflow source itself is trusted. PR merge evidence is separate.
type githubEvidencePolicy struct {
	binding    githubBinding
	branch     string
	sha        string
	workflowID int64
	name       string
	path       string
	required   []string
}

type githubCIEvidence struct {
	policy     githubEvidencePolicy
	runID      int64
	attempt    int64
	jobIDs     []int64
	observedAt time.Time
}

const githubEvidenceLifetime = 30 * time.Second

func (p githubEvidencePolicy) valid() bool {
	if !resourceUUID.MatchString(p.binding.tenantID) || p.binding.installationID <= 0 ||
		!(projectCreate{Name: "verify", RepositoryID: p.binding.repositoryID, Repository: p.binding.fullName, CodeBranch: p.branch}).valid() ||
		!taskSHA.MatchString(p.sha) || p.workflowID <= 0 || p.name == "" || strings.TrimSpace(p.name) != p.name ||
		!strings.HasPrefix(p.path, ".github/workflows/") || len(p.required) == 0 || len(p.required) > 100 {
		return false
	}
	file := strings.TrimPrefix(p.path, ".github/workflows/")
	if strings.ContainsAny(file, "/\\") || file == ".yml" || file == ".yaml" || (!strings.HasSuffix(file, ".yml") && !strings.HasSuffix(file, ".yaml")) {
		return false
	}
	seen := make(map[string]bool)
	for _, name := range p.required {
		if name == "" || strings.TrimSpace(name) != name || seen[name] {
			return false
		}
		seen[name] = true
	}
	return true
}

// Only push evidence is accepted here. A PR run must be bound to its
// independently verified head/base/merge relationship in a later batch.
func makeGitHubPushEvidence(p githubEvidencePolicy, workflow githubWorkflow, run githubRun, jobs []githubJob, observedAt time.Time) (githubCIEvidence, error) {
	if !p.valid() || observedAt.IsZero() || workflow.ID != p.workflowID || workflow.Name != p.name ||
		workflow.Path != p.path || workflow.State != "active" || run.WorkflowID != p.workflowID ||
		run.Event != "push" || run.HeadBranch != p.branch || run.HeadSHA != p.sha ||
		run.Repository.ID != p.binding.repositoryID || run.HeadRepository.ID != p.binding.repositoryID ||
		classifyGitHubCI(run, jobs, p.required) != githubCIPassed {
		return githubCIEvidence{}, errGitHubAuthorization
	}
	p.required = append([]string(nil), p.required...)
	sort.Strings(p.required)
	ids := make([]int64, 0, len(jobs))
	for _, j := range jobs {
		ids = append(ids, j.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return githubCIEvidence{p, run.ID, run.Attempt, ids, observedAt}, nil
}

func (e githubCIEvidence) freshFor(p githubEvidencePolicy, now time.Time) bool {
	if !p.valid() || !e.policy.valid() || e.runID <= 0 || e.attempt <= 0 || len(e.jobIDs) == 0 ||
		e.observedAt.IsZero() || now.Before(e.observedAt) || !now.Before(e.observedAt.Add(githubEvidenceLifetime)) ||
		e.policy.binding != p.binding || e.policy.branch != p.branch || e.policy.sha != p.sha ||
		e.policy.workflowID != p.workflowID || e.policy.name != p.name || e.policy.path != p.path ||
		len(e.policy.required) != len(p.required) {
		return false
	}
	required := append([]string(nil), p.required...)
	sort.Strings(required)
	for i, name := range required {
		if e.policy.required[i] != name {
			return false
		}
	}
	return true
}

// Re-read the full workflow/run listing after reading jobs. A newer run,
// changed workflow identity, or superseded attempt invalidates the snapshot.
func (a *githubApp) observePushEvidence(ctx context.Context, p githubEvidencePolicy, clock func() time.Time) (githubCIEvidence, error) {
	if !p.valid() || clock == nil {
		return githubCIEvidence{}, errGitHubAuthorization
	}
	started := clock()
	selectRun := func(now time.Time) (githubWorkflow, githubRun, error) {
		workflows, runs, err := a.listActions(ctx, p.binding, p.branch, p.sha, now)
		if err != nil {
			return githubWorkflow{}, githubRun{}, err
		}
		var w githubWorkflow
		var r githubRun
		for _, item := range workflows {
			if item.ID == p.workflowID {
				w = item
			}
		}
		for _, item := range runs {
			if item.WorkflowID == p.workflowID && item.Event == "push" {
				if r.ID != 0 {
					return w, githubRun{}, errGitHubUnavailable
				}
				r = item
			}
		}
		if w.ID != p.workflowID || w.Name != p.name || w.Path != p.path || w.State != "active" || r.ID == 0 {
			return w, r, errGitHubAuthorization
		}
		return w, r, nil
	}
	w, r, err := selectRun(started)
	if err != nil {
		return githubCIEvidence{}, err
	}
	jobs, err := a.listAttemptJobs(ctx, p.binding, r, clock())
	if err != nil {
		return githubCIEvidence{}, err
	}
	currentW, currentR, err := selectRun(clock())
	if err != nil {
		return githubCIEvidence{}, err
	}
	finished := clock()
	if currentW != w || currentR != r || finished.Before(started) || finished.Sub(started) >= githubEvidenceLifetime || ctx.Err() != nil {
		return githubCIEvidence{}, errGitHubUnavailable
	}
	return makeGitHubPushEvidence(p, w, r, jobs, started)
}
