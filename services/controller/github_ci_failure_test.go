package main

import (
	"strings"
	"testing"
)

func TestGitHubRequiredFailures(t *testing.T) {
	run := githubRun{ID: 81, Attempt: 2, HeadSHA: strings.Repeat("a", 40), Status: "completed", Conclusion: "failure"}
	job := githubJob{91, 81, 2, run.HeadSHA, "gate", "completed", "failure"}
	if ids, err := githubRequiredFailures(run, []githubJob{job}, []string{"gate"}); err != nil || len(ids) != 1 || ids[0] != 91 {
		t.Fatal("required failure rejected", err)
	}
	for _, conclusion := range []string{"success", "cancelled", "timed_out", "startup_failure", "action_required", "stale", ""} {
		bad := run
		bad.Conclusion = conclusion
		if _, err := githubRequiredFailures(bad, []githubJob{job}, []string{"gate"}); err == nil {
			t.Fatal("nonfailure run accepted", conclusion)
		}
	}
	for _, conclusion := range []string{"success", "skipped", "cancelled", "timed_out", "neutral", ""} {
		bad := job
		bad.Conclusion = conclusion
		if _, err := githubRequiredFailures(run, []githubJob{bad}, []string{"gate"}); err == nil {
			t.Fatal("nonfailure job accepted", conclusion)
		}
	}
	bad := job
	bad.RunAttempt++
	if _, err := githubRequiredFailures(run, []githubJob{bad}, []string{"gate"}); err == nil {
		t.Fatal("wrong attempt accepted")
	}
	bad = job
	bad.Status = "in_progress"
	if _, err := githubRequiredFailures(run, []githubJob{bad}, []string{"gate"}); err == nil {
		t.Fatal("running job accepted")
	}
	if _, err := githubRequiredFailures(run, []githubJob{job, job}, []string{"gate"}); err == nil {
		t.Fatal("duplicate job accepted")
	}
	if _, err := githubRequiredFailures(run, []githubJob{job}, []string{"gate", "backend"}); err == nil {
		t.Fatal("missing required job accepted")
	}
}
