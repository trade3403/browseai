package main

import (
	"context"
	"time"
)

// This service accepts only the authenticated principal and project ID.
// The immutable project branch and trusted installation binding are read
// from storage, never from external request parameters.
type githubProjectObserver struct {
	projects projectStore
	bindings githubBindingStore
	app      *githubApp
}

func (s githubProjectObserver) observe(ctx context.Context, p principal, projectID string, now time.Time) (githubBranchObservation, error) {
	if s.app == nil || !resourceUUID.MatchString(projectID) {
		return githubBranchObservation{}, errGitHubAuthorization
	}
	project, err := s.projects.get(ctx, p, projectID)
	if err != nil {
		return githubBranchObservation{}, err
	}
	binding, err := s.bindings.forProject(ctx, p, projectID)
	if err != nil {
		return githubBranchObservation{}, err
	}
	observed, err := s.app.observeBranch(ctx, binding, project.CodeBranch, now)
	if err != nil {
		return githubBranchObservation{}, err
	}
	// Do not return an observation if the local authorization changed
	// during the network read. Later transitions must revalidate as well.
	current, err := s.bindings.forProject(ctx, p, projectID)
	if err != nil || current != binding {
		return githubBranchObservation{}, errGitHubAuthorization
	}
	return observed, nil
}
