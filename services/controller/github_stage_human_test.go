package main

import (
	"context"
	"errors"
	"testing"
)

func TestGitHubStageHumanRejectsUnavailable(t *testing.T) {
	const tenant = "00000000-0000-4000-8000-000000000001"
	const task = "00000000-0000-4000-8000-000000000002"
	const acceptance = "00000000-0000-4000-8000-000000000003"
	const decision = "00000000-0000-4000-8000-000000000004"
	if err := reviewAuthorizedGitHubStage(context.Background(), nil, "", tenant, task, acceptance, decision, 2, actionComplete, true); !errors.Is(err, errGitHubAuthorization) {
		t.Fatal("unavailable session or database accepted", err)
	}
}
