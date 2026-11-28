package main

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

type githubRunPRLink struct {
	Number int64       `json:"number"`
	Head   githubPRRef `json:"head"`
	Base   githubPRRef `json:"base"`
}

type githubPRRunSnapshot struct {
	githubRun
	Path         string            `json:"path"`
	PullRequests []githubRunPRLink `json:"pull_requests"`
}

// A candidate proves association with the PR, NOT that the runner checked
// out or tested the synthetic merge commit. It cannot be consumed as a pass.
type githubPRCICandidate struct {
	relationship githubPRRelationship
	policy       githubPRPolicy
	runID        int64
	attempt      int64
	jobIDs       []int64
	observedAt   time.Time
}

func validGitHubPRRun(p githubPRPolicy, rel githubPRRelationship, w githubWorkflow, snapshot githubPRRunSnapshot) bool {
	r := snapshot.githubRun
	if !p.valid() || rel.number != p.number || rel.headSHA != p.push.sha || rel.baseSHA != p.baseSHA ||
		!taskSHA.MatchString(rel.mergeSHA) || rel.mergeSHA == rel.headSHA || rel.mergeSHA == rel.baseSHA ||
		w.ID != p.push.workflowID || w.Name != p.push.name || w.Path != p.push.path || w.State != "active" ||
		r.ID <= 0 || r.Attempt <= 0 || r.WorkflowID != w.ID || snapshot.Path != w.Path ||
		r.Event != "pull_request" || r.HeadBranch != p.push.branch || r.HeadSHA != rel.headSHA ||
		r.Repository.ID != p.push.binding.repositoryID || r.HeadRepository.ID != p.push.binding.repositoryID ||
		len(snapshot.PullRequests) != 1 {
		return false
	}
	link := snapshot.PullRequests[0]
	return link.Number == p.number && link.Head.Ref == p.push.branch && link.Head.SHA == rel.headSHA &&
		link.Base.Ref == p.baseBranch && link.Base.SHA == rel.baseSHA &&
		link.Head.Repo.ID == p.push.binding.repositoryID && link.Base.Repo.ID == p.push.binding.repositoryID
}

func makeGitHubPRCandidate(p githubPRPolicy, rel githubPRRelationship, w githubWorkflow, snapshot githubPRRunSnapshot, jobs []githubJob, observedAt time.Time) (githubPRCICandidate, error) {
	if observedAt.IsZero() || !validGitHubPRRun(p, rel, w, snapshot) ||
		classifyGitHubCI(snapshot.githubRun, jobs, p.push.required) != githubCIPassed {
		return githubPRCICandidate{}, errGitHubAuthorization
	}
	p.push.required = append([]string(nil), p.push.required...)
	sort.Strings(p.push.required)
	ids := make([]int64, 0, len(jobs))
	for _, job := range jobs {
		ids = append(ids, job.ID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return githubPRCICandidate{rel, p, snapshot.ID, snapshot.Attempt, ids, observedAt}, nil
}

func (a *githubApp) readPRRunSnapshot(ctx context.Context, p githubPRPolicy, runID int64, now time.Time) (githubPRRunSnapshot, error) {
	if !p.valid() || runID <= 0 {
		return githubPRRunSnapshot{}, errGitHubAuthorization
	}
	token, err := a.mint(ctx, p.push.binding, now)
	if err != nil {
		return githubPRRunSnapshot{}, err
	}
	parts := strings.Split(p.push.binding.fullName, "/")
	path := "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
	var result githubPRRunSnapshot
	if err := a.readJSON(ctx, token, fmt.Sprintf("%s/actions/runs/%d", path, runID), nil, now, &result); err != nil {
		return githubPRRunSnapshot{}, err
	}
	if result.ID != runID {
		return githubPRRunSnapshot{}, errGitHubAuthorization
	}
	return result, nil
}

func (a *githubApp) observePRCandidate(ctx context.Context, p githubPRPolicy, clock func() time.Time) (githubPRCICandidate, error) {
	if !p.valid() || clock == nil {
		return githubPRCICandidate{}, errGitHubAuthorization
	}
	started := clock()
	rel, err := a.observePRRelationship(ctx, p, clock)
	if err != nil {
		return githubPRCICandidate{}, err
	}
	selectRun := func() (githubWorkflow, githubRun, error) {
		workflows, runs, err := a.listActions(ctx, p.push.binding, p.push.branch, p.push.sha, clock())
		if err != nil {
			return githubWorkflow{}, githubRun{}, err
		}
		var w githubWorkflow
		var r githubRun
		for _, item := range workflows {
			if item.ID == p.push.workflowID {
				w = item
			}
		}
		for _, item := range runs {
			if item.WorkflowID == p.push.workflowID && item.Event == "pull_request" {
				if r.ID != 0 {
					return w, githubRun{}, errGitHubUnavailable
				}
				r = item
			}
		}
		if w.ID != p.push.workflowID || r.ID == 0 {
			return w, r, errGitHubUnavailable
		}
		return w, r, nil
	}
	w, r, err := selectRun()
	if err != nil {
		return githubPRCICandidate{}, err
	}
	snapshot, err := a.readPRRunSnapshot(ctx, p, r.ID, clock())
	if err != nil {
		return githubPRCICandidate{}, err
	}
	if snapshot.githubRun != r || !validGitHubPRRun(p, rel, w, snapshot) {
		return githubPRCICandidate{}, errGitHubAuthorization
	}
	jobs, err := a.listAttemptJobs(ctx, p.push.binding, r, clock())
	if err != nil {
		return githubPRCICandidate{}, err
	}
	currentRel, err := a.observePRRelationship(ctx, p, clock)
	if err != nil {
		return githubPRCICandidate{}, err
	}
	currentW, currentR, err := selectRun()
	if err != nil {
		return githubPRCICandidate{}, err
	}
	current, err := a.readPRRunSnapshot(ctx, p, r.ID, clock())
	if err != nil {
		return githubPRCICandidate{}, err
	}
	finished := clock()
	if currentRel != rel || currentW != w || currentR != r || current.githubRun != r ||
		!validGitHubPRRun(p, rel, w, current) || finished.Before(started) ||
		finished.Sub(started) >= githubEvidenceLifetime || ctx.Err() != nil {
		return githubPRCICandidate{}, errGitHubUnavailable
	}
	return makeGitHubPRCandidate(p, rel, w, current, jobs, started)
}
