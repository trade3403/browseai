package main

import (
	"context"
	"slices"
	"sort"
	"time"
)

// Build the observer from a trusted, read-only GitHub App and independently
// pinned policy. Never accept an observer, workflow digest or policy from an
// incoming signing request.
func newGitHubTrustedSignerObserver(app *githubApp, policy githubIndependentPolicy, clock func() time.Time) (githubSupervisorObserve, error) {
	if app == nil || clock == nil || !policy.execution.valid() || !githubWorkflowDigestValid(policy.sourceDigest) {
		return nil, errGitHubAuthorization
	}
	p := policy.execution
	p.required = append([]string(nil), p.required...)
	sort.Strings(p.required)
	digest := policy.sourceDigest
	var pr *githubPRPolicy
	if policy.pr != nil {
		copyPR := *policy.pr
		copyPR.push.required = append([]string(nil), copyPR.push.required...)
		sort.Strings(copyPR.push.required)
		if !copyPR.valid() || copyPR.push.binding != p.binding || copyPR.push.branch != p.branch ||
			copyPR.push.sha != p.sha || copyPR.push.workflowID != p.workflowID ||
			copyPR.push.name != p.name || copyPR.push.path != p.path ||
			!slices.Equal(copyPR.push.required, p.required) {
			return nil, errGitHubAuthorization
		}
		pr = &copyPR
	}
	return func(ctx context.Context) (githubCIEvidence, githubPRRelationship, error) {
		fail := func() (githubCIEvidence, githubPRRelationship, error) {
			return githubCIEvidence{}, githubPRRelationship{}, errGitHubAuthorization
		}
		if ctx.Err() != nil {
			return fail()
		}
		start := clock()
		ctx, cancel := context.WithTimeout(ctx, githubEvidenceLifetime)
		defer cancel()
		var e githubCIEvidence
		var rel githubPRRelationship
		if pr == nil {
			var err error
			e, err = app.observePushEvidence(ctx, p, clock)
			if err != nil {
				return fail()
			}
			if err := app.verifyPushWorkflowSource(ctx, p, digest, clock()); err != nil {
				return fail()
			}
		} else {
			c, err := app.observePRCandidate(ctx, *pr, clock)
			if err != nil {
				return fail()
			}
			rel = c.relationship
			e = githubCIEvidence{c.policy.push, c.runID, c.attempt, c.jobIDs, c.observedAt}
			if err := app.verifyPRWorkflowSource(ctx, *pr, rel, digest, clock()); err != nil {
				return fail()
			}
		}
		now := clock()
		if ctx.Err() != nil || now.Before(start) || now.Sub(start) >= githubEvidenceLifetime || !e.freshFor(p, now) {
			return fail()
		}
		// Do not expose the closure's policy slices to the caller.
		e.policy.required = append([]string(nil), e.policy.required...)
		e.jobIDs = append([]int64(nil), e.jobIDs...)
		return e, rel, nil
	}, nil
}
