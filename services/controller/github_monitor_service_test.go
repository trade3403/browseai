package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestGitHubMonitorPolicy(t *testing.T) {
	const tenant = "00000000-0000-4000-8000-000000000001"
	const raw = `[{"tenant_id":"` + tenant + `","installation_id":12,"repository_id":34,"workflow_id":56,"event":"push","required_jobs":["gate","backend"]}]`
	policies, err := parseGitHubMonitorPolicies([]byte(raw))
	if err != nil || len(policies) != 1 {
		t.Fatal("policy rejected", err)
	}
	for _, bad := range []string{
		"[]", "null", raw + "{}",
		strings.Replace(raw, "\"push\"", "\"other\"", 1),
		strings.Replace(raw, "\"gate\",\"backend\"", "\"gate\",\"gate\"", 1),
		strings.Replace(raw, "\"gate\"", "\" gate\"", 1),
		strings.Replace(raw, "\"gate\",\"backend\"", "", 1),
		strings.Replace(raw, "\"workflow_id\":56", "\"workflow_id\":0", 1),
		strings.Replace(raw, "\"workflow_id\":56", "\"workflow_id\":56,\"unknown\":true", 1),
		raw[:len(raw)-1] + "," + raw[1:],
	} {
		if _, err := parseGitHubMonitorPolicies([]byte(bad)); err == nil {
			t.Fatal("invalid policy accepted")
		}
	}
	calls := 0
	read := githubMonitorPolicyReader(policies, func(_ context.Context, b githubBinding, branch, sha string, workflow int64, event string, jobs []string, _ time.Time) (githubCIState, error) {
		calls++
		if len(jobs) != 2 || jobs[0] != "gate" || b.repositoryID != 34 || workflow != 56 || event != "push" {
			t.Fatal("wrong trusted policy")
		}
		jobs[0] = "mutated"
		return githubCIPending, nil
	})
	m := githubMonitorClaim{binding: githubBinding{tenant, 12, 34, "owner/repo"}, workflowID: 56, event: "push"}
	for i := 0; i < 2; i++ {
		if state, err := read(context.Background(), m, time.Now()); state != githubCIPending || err != nil {
			t.Fatal("trusted target rejected")
		}
	}
	m.binding.repositoryID++
	if _, err := read(context.Background(), m, time.Now()); !errors.Is(err, errGitHubAuthorization) || calls != 2 {
		t.Fatal("unconfigured target queried")
	}
}

func TestGitHubMonitorService(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	var delays []time.Duration
	err := runGitHubMonitorService(ctx, func(context.Context) (bool, error) {
		calls++
		switch calls {
		case 1:
			return false, nil
		case 2:
			return true, errGitHubUnavailable
		case 3:
			return true, nil
		default:
			cancel()
			return false, nil
		}
	}, func(_ context.Context, d time.Duration) error {
		delays = append(delays, d)
		return nil
	})
	if !errors.Is(err, context.Canceled) || len(delays) != 3 || delays[0] != 5*time.Second || delays[1] != 2*time.Second || delays[2] != time.Second {
		t.Fatal("unexpected worker schedule", delays, err)
	}
	calls = 0
	err = runGitHubMonitorService(context.Background(), func(context.Context) (bool, error) {
		calls++
		return false, errGitHubUnavailable
	}, func(context.Context, time.Duration) error { return nil })
	if !errors.Is(err, errGitHubUnavailable) || calls != 8 {
		t.Fatal("service failure budget ignored")
	}
}
