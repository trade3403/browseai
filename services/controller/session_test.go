package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeSessions struct {
	rows   map[sessionDigest]principal
	deny   bool
	failed error
}

func (f *fakeSessions) issue(_ context.Context, p principal, d sessionDigest) (bool, error) {
	if f.failed != nil || f.deny {
		return false, f.failed
	}
	f.rows[d] = p
	return true, nil
}

func (f *fakeSessions) resolve(_ context.Context, d sessionDigest) (principal, error) {
	if f.failed != nil {
		return principal{}, f.failed
	}
	p, ok := f.rows[d]
	if !ok {
		return principal{}, errUnauthenticated
	}
	return p, nil
}

func (f *fakeSessions) revoke(_ context.Context, d sessionDigest) error {
	if f.failed != nil {
		return f.failed
	}
	delete(f.rows, d)
	return nil
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	f := &fakeSessions{rows: make(map[sessionDigest]principal)}
	s := sessionService{store: f}
	a := principal{"user-a", "tenant-a", roleUser}
	b := principal{"user-b", "tenant-b", roleUser}
	first, err := s.issueVerified(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.issueVerified(ctx, b)
	if err != nil || first == second {
		t.Fatal("tokens must differ", err)
	}
	for _, tc := range []struct {
		token string
		want  principal
	}{{first, a}, {second, b}} {
		digest, err := digestSession(tc.token)
		if err != nil || f.rows[digest] != tc.want {
			t.Fatal("digest not stored", err)
		}
		got, err := s.authenticate(ctx, tc.token)
		if err != nil || got != tc.want {
			t.Fatal("identity mismatch", err)
		}
	}
	if err := s.logout(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.authenticate(ctx, first); !errors.Is(err, errUnauthenticated) {
		t.Fatal("revoked token accepted", err)
	}
	if got, err := s.authenticate(ctx, second); err != nil || got != b {
		t.Fatal("other tenant affected", err)
	}
}

func TestSessionDenialAndFailures(t *testing.T) {
	ctx := context.Background()
	f := &fakeSessions{rows: make(map[sessionDigest]principal)}
	s := sessionService{store: f}
	p := principal{"user", "tenant", roleUser}
	for _, tok := range []string{"", "invalid", strings.Repeat("!", 43), strings.Repeat("A", 42) + "B", strings.Repeat("A", 43)} {
		if _, err := s.authenticate(ctx, tok); !errors.Is(err, errUnauthenticated) {
			t.Fatal("invalid token accepted", err)
		}
	}
	if err := s.logout(ctx, "bad"); !errors.Is(err, errUnauthenticated) {
		t.Fatal(err)
	}
	for _, bad := range []principal{{}, {"user", "tenant", "unknown"}} {
		if _, err := s.issueVerified(ctx, bad); !errors.Is(err, errUnauthenticated) {
			t.Fatal("invalid principal", err)
		}
	}
	f.deny = true
	if _, err := s.issueVerified(ctx, p); !errors.Is(err, errUnauthenticated) {
		t.Fatal(err)
	}
	f.deny = false
	token, err := s.issueVerified(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := digestSession(token)
	if err != nil {
		t.Fatal(err)
	}
	f.rows[digest] = principal{"user", "tenant", "unknown"}
	if _, err := s.authenticate(ctx, token); !errors.Is(err, errUnauthenticated) {
		t.Fatal("invalid stored role accepted", err)
	}
	f.failed = errors.New("database unavailable")
	if _, err := s.issueVerified(ctx, p); !errors.Is(err, f.failed) {
		t.Fatal(err)
	}
	if _, err := s.authenticate(ctx, token); !errors.Is(err, f.failed) {
		t.Fatal(err)
	}
	if err := s.logout(ctx, token); !errors.Is(err, f.failed) {
		t.Fatal(err)
	}
}
