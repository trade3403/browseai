package main

import (
	"errors"
	"testing"
)

func TestTenantProjectIsolation(t *testing.T) {
	records := []ownedProject{
		{"tenant-a", Project{ID: "a-private"}},
		{"tenant-b", Project{ID: "b-private"}},
	}
	for _, tc := range []struct{ tenant, id string }{{"tenant-a", "a-private"}, {"tenant-b", "b-private"}} {
		got, err := listProjects(principal{"user", tc.tenant, roleUser}, records)
		if err != nil || len(got) != 1 || got[0].ID != tc.id {
			t.Fatalf("tenant isolation failed: %v %v", got, err)
		}
	}
	got, err := listProjects(principal{"user", "tenant-empty", roleUser}, records)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty tenant: %v %v", got, err)
	}
}

func TestProjectScopeDefaultDeny(t *testing.T) {
	for _, p := range []principal{{}, {"user", "", roleUser}, {"", "tenant-a", roleUser}, {"admin", "tenant-a", rolePlatformAdmin}, {"user", "tenant-a", "unknown"}} {
		if got, err := listProjects(p, []ownedProject{{"tenant-a", Project{ID: "private"}}}); !errors.Is(err, errForbidden) || got != nil {
			t.Fatalf("unexpected access: %v %v", got, err)
		}
	}
}
