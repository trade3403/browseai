package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGitHubPollBackoff(t *testing.T) {
	now := time.Now()
	p := nextGitHubPoll(githubPollResult{}, githubCIPassed, nil, now)
	if p.state != githubCIPassed || p.failures != 0 || p.nextAt.Sub(now) != 30*time.Second {
		t.Fatal("unexpected success schedule", p)
	}
	for i := 1; i <= 12; i++ {
		p = nextGitHubPoll(p, githubCIPassed, errGitHubUnavailable, now)
		if p.state != githubCIUnknown || p.nextAt.Sub(now) > githubPollMaxBackoff || p.failures > githubPollMaxFailures {
			t.Fatal("failed read retained success or exceeded bound", p)
		}
	}
	limited := now.Add(time.Hour)
	p = nextGitHubPoll(p, githubCIPassed, githubReadError{retryAt: limited}, now)
	if !p.nextAt.Equal(limited) || p.state != githubCIUnknown {
		t.Fatal("upstream retry deadline ignored")
	}
	p = nextGitHubPoll(p, githubCIPending, nil, now)
	if p.failures != 0 || p.nextAt.Sub(now) != githubPollInterval {
		t.Fatal("backoff not reset")
	}
}

func TestGitHubPollWorker(t *testing.T) {
	now := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	var published []githubPollResult
	reads := 0
	err := runGitHubPoll(ctx, func(ctx context.Context, at time.Time) (githubCIState, error) {
		reads++
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing read deadline")
		}
		switch reads {
		case 1:
			return githubCIPassed, nil
		case 2:
			return githubCIPassed, githubReadError{retryAt: at.Add(5 * time.Minute)}
		default:
			return githubCIUnknown, errGitHubAuthorization
		}
	}, func(p githubPollResult) error {
		published = append(published, p)
		return nil
	}, func() time.Time { return now }, func(_ context.Context, delay time.Duration) error {
		now = now.Add(delay)
		return nil
	})
	if !errors.Is(err, errGitHubAuthorization) || reads != 3 || len(published) != 3 {
		t.Fatal("unexpected worker result", err, reads)
	}
	if published[1].state != githubCIUnknown || published[1].nextAt.Sub(published[1].observedAt) != 5*time.Minute {
		t.Fatal("stale success or ignored rate limit")
	}
}

func TestGitHubPollStops(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	now := time.Now()
	reads := 0
	read := func(context.Context, time.Time) (githubCIState, error) {
		reads++
		return githubCIUnknown, errGitHubUnavailable
	}
	publish := func(githubPollResult) error { return nil }
	clock := func() time.Time { return now }
	wait := func(context.Context, time.Duration) error { return nil }
	if err := runGitHubPoll(ctx, read, publish, clock, wait); !errors.Is(err, errGitHubUnavailable) || reads != githubPollMaxFailures {
		t.Fatal("failure budget not enforced", reads, err)
	}
	if err := runGitHubPoll(context.Background(), read, publish, clock, wait); !errors.Is(err, errGitHubAuthorization) {
		t.Fatal("unbounded worker accepted")
	}
	cancel()
	if err := runGitHubPoll(ctx, read, publish, clock, wait); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled worker accepted", err)
	}
	if err := waitGitHubPoll(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatal("wait cancellation lost", err)
	}
}
