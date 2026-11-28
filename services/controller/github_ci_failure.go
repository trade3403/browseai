package main

import (
	"context"
	"sort"
	"strings"
	"time"
)

// A failure candidate is not execution attestation or permission to send
// repair instructions. The state-machine adapter must separately authorize
// the task, revision, policy, fresh observation and human approval.
type githubCIFailureCandidate struct {
	task       githubEvidenceTask
	policy     githubEvidencePolicy
	event      string
	runID      int64
	attempt    int64
	failedIDs  []int64
	observedAt time.Time
}

// Require an actual failed required job, not just a failed workflow or
// infrastructure timeout. Unknown, skipped-only or cancelled results do not
// trigger code repair. All listed jobs must belong to the exact attempt.
func githubRequiredFailures(run githubRun, jobs []githubJob, required []string) ([]int64, error) {
	if run.ID <= 0 || run.Attempt <= 0 || !taskSHA.MatchString(run.HeadSHA) ||
		run.Status != "completed" || run.Conclusion != "failure" ||
		len(jobs) == 0 || len(jobs) > 1000 || len(required) == 0 || len(required) > 100 {
		return nil, errGitHubAuthorization
	}
	expected := make(map[string]bool)
	for _, name := range required {
		if name == "" || strings.TrimSpace(name) != name || expected[name] {
			return nil, errGitHubAuthorization
		}
		expected[name] = true
	}
	seen := make(map[string]bool)
	ids := make(map[int64]bool)
	var failed []int64
	for _, job := range jobs {
		if job.ID <= 0 || ids[job.ID] || job.RunID != run.ID || job.RunAttempt != run.Attempt ||
			job.HeadSHA != run.HeadSHA || job.Name == "" || seen[job.Name] || job.Status != "completed" {
			return nil, errGitHubAuthorization
		}
		seen[job.Name], ids[job.ID] = true, true
		switch job.Conclusion {
		case "success", "skipped":
		case "failure":
			if expected[job.Name] {
				failed = append(failed, job.ID)
			}
		default:
			return nil, errGitHubAuthorization
		}
	}
	for name := range expected {
		if !seen[name] {
			return nil, errGitHubAuthorization
		}
	}
	if len(failed) == 0 {
		return nil, errGitHubAuthorization
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i] < failed[j] })
	return failed, nil
}

// Re-observe one exact event, including immutable workflow source and the PR
// merge relationship when applicable. No caller-provided job results or
// webhook conclusion can construct this candidate through the adapter.
func (a *githubApp) observeCIFailure(ctx context.Context, policy githubIndependentPolicy, clock func() time.Time) (githubCIFailureCandidate, error) {
	fail := func() (githubCIFailureCandidate, error) { return githubCIFailureCandidate{}, errGitHubAuthorization }
	if a == nil || clock == nil || ctx.Err() != nil || !policy.task.valid() {
		return fail()
	}
	// Validate the same pinned Push/PR policy contract as the signer.
	if _, err := newGitHubTrustedSignerObserver(a, policy, clock); err != nil {
		return fail()
	}
	p := policy.execution
	p.required = append([]string(nil), p.required...)
	sort.Strings(p.required)
	event := "push"
	if policy.pr != nil {
		event = "pull_request"
		frozen := *policy.pr
		frozen.push.required = append([]string(nil), frozen.push.required...)
		policy.pr = &frozen
	}
	ctx, cancel := context.WithTimeout(ctx, githubEvidenceLifetime)
	defer cancel()
	start := clock()
	selectRun := func() (githubWorkflow, githubRun, error) {
		workflows, runs, err := a.listActions(ctx, p.binding, p.branch, p.sha, clock())
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
			if item.WorkflowID == p.workflowID && item.Event == event {
				if r.ID != 0 {
					return w, githubRun{}, errGitHubUnavailable
				}
				r = item
			}
		}
		if w.ID != p.workflowID || w.Name != p.name || w.Path != p.path || w.State != "active" ||
			r.ID == 0 || r.Status != "completed" || r.Conclusion != "failure" {
			return w, r, errGitHubAuthorization
		}
		return w, r, nil
	}
	w, r, err := selectRun()
	if err != nil {
		return fail()
	}
	checkPR := func(rel githubPRRelationship) error {
		snapshot, err := a.readPRRunSnapshot(ctx, *policy.pr, r.ID, clock())
		if err != nil || snapshot.githubRun != r || !validGitHubPRRun(*policy.pr, rel, w, snapshot) {
			return errGitHubAuthorization
		}
		return nil
	}
	var rel githubPRRelationship
	if policy.pr != nil {
		rel, err = a.observePRRelationship(ctx, *policy.pr, clock)
		if err != nil || checkPR(rel) != nil {
			return fail()
		}
	}
	jobs, err := a.listAttemptJobs(ctx, p.binding, r, clock())
	if err != nil {
		return fail()
	}
	failed, err := githubRequiredFailures(r, jobs, p.required)
	if err != nil {
		return fail()
	}
	if policy.pr == nil {
		err = a.verifyPushWorkflowSource(ctx, p, policy.sourceDigest, clock())
	} else {
		err = a.verifyPRWorkflowSource(ctx, *policy.pr, rel, policy.sourceDigest, clock())
	}
	if err != nil {
		return fail()
	}
	if policy.pr != nil {
		current, err := a.observePRRelationship(ctx, *policy.pr, clock)
		if err != nil || current != rel || checkPR(current) != nil {
			return fail()
		}
	}
	currentW, currentR, err := selectRun()
	now := clock()
	if err != nil || currentW != w || currentR != r || start.IsZero() ||
		now.Before(start) || now.Sub(start) >= githubEvidenceLifetime || ctx.Err() != nil {
		return fail()
	}
	return githubCIFailureCandidate{policy.task, p, event, r.ID, r.Attempt, failed, start}, nil
}
