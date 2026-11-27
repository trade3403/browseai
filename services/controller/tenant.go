package main

import "errors"

type role string

const (
	roleUser          role = "user"
	rolePlatformAdmin role = "platform_admin"
)

var errForbidden = errors.New("access denied")

type principal struct {
	userID   string
	tenantID string
	role     role
}

type ownedProject struct {
	tenantID string
	project  Project
}

// listProjects is a service-layer boundary, not an authentication mechanism.
// Its principal must be derived from verified server-side identity, never a request header.
func listProjects(p principal, records []ownedProject) ([]Project, error) {
	if p.userID == "" || p.tenantID == "" || p.role != roleUser {
		return nil, errForbidden
	}
	out := make([]Project, 0)
	for _, row := range records {
		if row.tenantID == p.tenantID {
			out = append(out, row.project)
		}
	}
	return out, nil
}
