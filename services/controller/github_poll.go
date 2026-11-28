package main

import (
	"context"
	"errors"
	"time"
)

// One trusted monitoring target per poll invocation. A pass is only an
// observation candidate, never authorization to advance the task.
type githubPollResult struct {
	state      githubCIState
	observedAt time.Time
	nextAt     time.Time
	failures   int
}

const githubPollInterval = 30 * time.Second
const githubPollMaxBackoff = 15 * time.Minute
const githubPollMaxFailures = 8
const githubPollReadTimeout = 90 * time.Second

func nextGitHubPoll(previous githubPollResult, state githubCIState, err error, now time.Time) githubPollResult {
	result := githubPollResult{state: state, observedAt: now, nextAt: now.Add(githubPollInterval)}
	if err == nil && state != githubCIUnknown {
		return result
	}
	// A failed read always invalidates a previously successful observation.
	result.state = githubCIUnknown
	result.failures = previous.failures
	if result.failures < githubPollMaxFailures {
		result.failures++
	}
	delay := githubPollInterval
	for i := 0; i < result.failures; i++ {
		delay *= 2
		if delay >= githubPollMaxBackoff {
			delay = githubPollMaxBackoff
			break
		}
	}
	result.nextAt = now.Add(delay)
	var limited githubReadError
	if errors.As(err, &limited) && limited.retryAt.After(result.nextAt) {
		result.nextAt = limited.retryAt
	}
	return result
}

type githubPollRead func(context.Context, time.Time) (githubCIState, error)
type githubPollPublish func(githubPollResult) error
type githubPollWait func(context.Context, time.Duration) error

func waitGitHubPoll(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// A bounded, cancellable polling worker for a single immutable target.
// No GitHub writes, no task transitions, and no goroutines outlive the caller.
// The caller owns durable scheduling, tenant isolation and publication.
func runGitHubPoll(ctx context.Context, read githubPollRead, publish githubPollPublish, now func() time.Time, wait githubPollWait) error {
	if read == nil || publish == nil || now == nil || wait == nil {
		return errGitHubAuthorization
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return errGitHubAuthorization
	}
	var previous githubPollResult
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		started := now()
		if !started.Before(deadline) {
			return context.DeadlineExceeded
		}
		attempt, cancel := context.WithTimeout(ctx, githubPollReadTimeout)
		state, err := read(attempt, started)
		if attempt.Err() != nil {
			err = attempt.Err()
		}
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		previous = nextGitHubPoll(previous, state, err, now())
		if publishErr := publish(previous); publishErr != nil {
			return publishErr
		}
		if errors.Is(err, errGitHubAuthorization) {
			return errGitHubAuthorization
		}
		if previous.failures >= githubPollMaxFailures {
			return errGitHubUnavailable
		}
		delay := previous.nextAt.Sub(now())
		if delay < 0 {
			delay = 0
		}
		if !previous.nextAt.Before(deadline) {
			return context.DeadlineExceeded
		}
		if err := wait(ctx, delay); err != nil {
			return err
		}
	}
}

// Select exactly one run for a configured workflow/event. Ambiguous
// observations never become a pass. PR linkage validation is a later gate.
func (a *githubApp) pollActions(ctx context.Context, binding githubBinding, branch, sha string, workflowID int64, event string, required []string, now time.Time) (githubCIState, error) {
	if workflowID <= 0 || (event != "push" && event != "pull_request") || len(required) == 0 {
		return githubCIUnknown, errGitHubAuthorization
	}
	workflows, runs, err := a.listActions(ctx, binding, branch, sha, now)
	if err != nil {
		return githubCIUnknown, err
	}
	active := false
	for _, workflow := range workflows {
		if workflow.ID == workflowID && workflow.State == "active" {
			active = true
		}
	}
	if !active {
		return githubCIUnknown, errGitHubAuthorization
	}
	var selected githubRun
	for _, run := range runs {
		if run.WorkflowID != workflowID || run.Event != event {
			continue
		}
		if selected.ID != 0 {
			return githubCIUnknown, errGitHubUnavailable
		}
		selected = run
	}
	if selected.ID == 0 {
		return githubCIPending, nil
	}
	if selected.Status != "completed" || selected.Conclusion != "success" {
		return classifyGitHubCI(selected, nil, required), nil
	}
	jobs, err := a.listAttemptJobs(ctx, binding, selected, now)
	if err != nil {
		return githubCIUnknown, err
	}
	return classifyGitHubCI(selected, jobs, required), nil
}
