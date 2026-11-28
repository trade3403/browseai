package main

import (
	"strings"
	"testing"
)

func TestGitHubApprovalInput(t *testing.T) {
	args := []string{"bind", "00000000-0000-4000-8000-000000000001", "123", "456", "owner/repo", "12", "34"}
	confirmation := "APPROVE " + strings.Join(args[1:], " ")
	a, err := githubApprovalInput(args, strings.NewReader(confirmation+"\n"))
	if err != nil || a.binding.installationID != 123 || a.binding.repositoryID != 456 || a.appID != 12 || a.accountID != 34 {
		t.Fatal("valid approval rejected", err)
	}
	for _, bad := range []string{"", "yes", confirmation + "\nextra", strings.Replace(confirmation, "456", "457", 1), strings.Repeat("x", 1025)} {
		if _, err := githubApprovalInput(args, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid approval accepted")
		}
	}
	for _, index := range []int{1, 2, 3, 4, 5, 6} {
		bad := append([]string(nil), args...)
		bad[index] = "0"
		if _, err := githubApprovalInput(bad, strings.NewReader("APPROVE "+strings.Join(bad[1:], " "))); err == nil {
			t.Fatal("invalid argument accepted", index)
		}
	}
}

func TestGitHubOperatorDSN(t *testing.T) {
	base := "postgres://browseai_github_operator@db.example/browseai?sslmode=verify-full"
	if !githubOperatorDSNValid(base) {
		t.Fatal("valid operator DSN rejected")
	}
	for _, bad := range []string{
		strings.Replace(base, "verify-full", "disable", 1),
		strings.Replace(base, "browseai_github_operator", "browseai_app", 1),
		base + "&sslmode=disable",
		base + "&user=postgres",
		base + "&host=other",
	} {
		if githubOperatorDSNValid(bad) {
			t.Fatal("unsafe DSN accepted")
		}
	}
}
