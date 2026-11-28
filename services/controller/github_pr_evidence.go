package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

type githubPRRef struct {
	Ref  string `json:"ref"`
	SHA  string `json:"sha"`
	Repo struct {
		ID int64 `json:"id"`
	} `json:"repo"`
}

type githubPullSnapshot struct {
	Number         int64       `json:"number"`
	State          string      `json:"state"`
	Merged         bool        `json:"merged"`
	Mergeable      *bool       `json:"mergeable"`
	MergeCommitSHA string      `json:"merge_commit_sha"`
	Head           githubPRRef `json:"head"`
	Base           githubPRRef `json:"base"`
}

type githubMergeCommit struct {
	SHA     string `json:"sha"`
	Parents []struct {
		SHA string `json:"sha"`
	} `json:"parents"`
}

// The expected PR number and base branch come from trusted policy, not
// from a run payload. Only open, same-repository PRs are supported.
type githubPRPolicy struct {
	push       githubEvidencePolicy
	number     int64
	baseBranch string
	baseSHA    string
}

type githubPRRelationship struct {
	number   int64
	headSHA  string
	baseSHA  string
	mergeSHA string
}

func (p githubPRPolicy) valid() bool {
	return p.push.valid() && p.number > 0 && p.baseBranch != p.push.branch &&
		taskSHA.MatchString(p.baseSHA) && p.baseSHA != p.push.sha &&
		(projectCreate{Name: "verify", RepositoryID: p.push.binding.repositoryID, Repository: p.push.binding.fullName, CodeBranch: p.baseBranch}).valid()
}

func validateGitHubPRRelationship(p githubPRPolicy, pr githubPullSnapshot, merge githubMergeCommit) (githubPRRelationship, error) {
	if !p.valid() || pr.Number != p.number || pr.State != "open" || pr.Merged || pr.Mergeable == nil || !*pr.Mergeable ||
		pr.Head.Repo.ID != p.push.binding.repositoryID || pr.Base.Repo.ID != p.push.binding.repositoryID ||
		pr.Head.Ref != p.push.branch || pr.Head.SHA != p.push.sha || pr.Base.Ref != p.baseBranch || pr.Base.SHA != p.baseSHA ||
		!taskSHA.MatchString(pr.MergeCommitSHA) || pr.MergeCommitSHA == p.push.sha || pr.MergeCommitSHA == p.baseSHA ||
		merge.SHA != pr.MergeCommitSHA || len(merge.Parents) != 2 ||
		merge.Parents[0].SHA != p.baseSHA || merge.Parents[1].SHA != p.push.sha {
		return githubPRRelationship{}, errGitHubAuthorization
	}
	return githubPRRelationship{p.number, p.push.sha, p.baseSHA, merge.SHA}, nil
}

// GitHub may compute mergeability asynchronously. Missing or changing
// merge commits are inconclusive, never a successful CI observation.
func (a *githubApp) observePRRelationship(ctx context.Context, p githubPRPolicy, clock func() time.Time) (githubPRRelationship, error) {
	if !p.valid() || clock == nil {
		return githubPRRelationship{}, errGitHubAuthorization
	}
	started := clock()
	token, err := a.mint(ctx, p.push.binding, started)
	if err != nil {
		return githubPRRelationship{}, err
	}
	parts := strings.Split(p.push.binding.fullName, "/")
	path := "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1])
	endpoint := fmt.Sprintf("%s/pulls/%d", path, p.number)
	var first, last githubPullSnapshot
	if err := a.readJSON(ctx, token, endpoint, nil, clock(), &first); err != nil {
		return githubPRRelationship{}, err
	}
	if !taskSHA.MatchString(first.MergeCommitSHA) {
		return githubPRRelationship{}, errGitHubUnavailable
	}
	var merge githubMergeCommit
	if err := a.readJSON(ctx, token, path+"/git/commits/"+first.MergeCommitSHA, nil, clock(), &merge); err != nil {
		return githubPRRelationship{}, err
	}
	initial, err := validateGitHubPRRelationship(p, first, merge)
	if err != nil {
		return githubPRRelationship{}, err
	}
	if err := a.readJSON(ctx, token, endpoint, nil, clock(), &last); err != nil {
		return githubPRRelationship{}, err
	}
	current, err := validateGitHubPRRelationship(p, last, merge)
	if err != nil || current != initial {
		return githubPRRelationship{}, errGitHubUnavailable
	}
	finished := clock()
	if finished.Before(started) || finished.Sub(started) >= githubEvidenceLifetime || ctx.Err() != nil {
		return githubPRRelationship{}, errGitHubUnavailable
	}
	return initial, nil
}
