package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"testing"
)

func TestGitHubVerifierRegistrationInput(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{
		"00000000-0000-4000-8000-000000000001", "12", "34",
		"00000000-0000-4000-8000-000000000002",
		"00000000-0000-4000-8000-000000000003", hex.EncodeToString(pub),
		"2026-11-28T00:00:00Z", "2026-12-01T00:00:00Z",
		"00000000-0000-4000-8000-000000000004",
	}
	approval := func(a []string) *strings.Reader {
		return strings.NewReader("APPROVE " + strings.Join(a, " ") + "\n")
	}
	if v, err := githubVerifierRegistrationInput(args, approval(args)); err != nil || !v.valid() {
		t.Fatal("valid registration rejected", err)
	}
	if _, err := githubVerifierRegistrationInput(args, strings.NewReader("APPROVE")); err == nil {
		t.Fatal("unbound approval accepted")
	}
	if _, err := githubVerifierRegistrationInput(args[:8], approval(args)); err == nil {
		t.Fatal("incomplete registration accepted")
	}
	for i, value := range map[int]string{
		0: "invalid", 1: "012", 2: "-1", 3: "invalid", 4: "invalid",
		5: strings.Repeat("00", 32), 6: "2026-11-28T01:00:00+01:00",
		7: "2027-12-01T00:00:00Z", 8: "invalid",
	} {
		bad := append([]string(nil), args...)
		bad[i] = value
		if _, err := githubVerifierRegistrationInput(bad, approval(bad)); err == nil {
			t.Fatal("invalid argument accepted", i)
		}
	}
}
