package main

import (
	"context"
	"sort"
	"time"
)

// Scheduling uses only complete Push/PR policy pairs from the protected
// configuration, with identical required jobs. Monitor results stay hints.
func githubMonitorSchedulePolicies(policies map[githubMonitorPolicyKey][]string) ([]githubMonitorPolicyKey, error) {
	if len(policies) == 0 || len(policies) > 100 {
		return nil, errGitHubAuthorization
	}
	var result []githubMonitorPolicyKey
	for key, jobs := range policies {
		other := key
		switch key.event {
		case "push":
			other.event = "pull_request"
		case "pull_request":
			other.event = "push"
		default:
			return nil, errGitHubAuthorization
		}
		paired, ok := policies[other]
		if !ok || len(jobs) == 0 || len(jobs) != len(paired) {
			return nil, errGitHubAuthorization
		}
		a, b := append([]string(nil), jobs...), append([]string(nil), paired...)
		sort.Strings(a)
		sort.Strings(b)
		for i := range a {
			if a[i] != b[i] {
				return nil, errGitHubAuthorization
			}
		}
		if key.event == "push" {
			result = append(result, key)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if a.tenant != b.tenant {
			return a.tenant < b.tenant
		}
		if a.installation != b.installation {
			return a.installation < b.installation
		}
		if a.repository != b.repository {
			return a.repository < b.repository
		}
		return a.workflow < b.workflow
	})
	return result, nil
}

func (s githubMonitorStore) schedulePair(ctx context.Context, p githubMonitorPolicyKey) (int, error) {
	if s.db == nil || !resourceUUID.MatchString(p.tenant) || p.installation <= 0 || p.repository <= 0 || p.workflow <= 0 || p.event != "push" {
		return 0, errGitHubAuthorization
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT browseai.schedule_github_monitor_pair($1,$2,$3,$4)", p.tenant, p.installation, p.repository, p.workflow).Scan(&count)
	if err != nil || count < 0 || count > 64 {
		return 0, errGitHubUnavailable
	}
	return count, nil
}
