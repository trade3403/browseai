package main

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

type githubRepairHandoff struct {
	Grant             string  `json:"grant_id"`
	Failure           string  `json:"failure_id"`
	Revision          int64   `json:"revision"`
	StageIndex        int     `json:"stage_index"`
	HeadSHA           string  `json:"head_sha"`
	WorkflowID        int64   `json:"workflow_id"`
	Event             string  `json:"event_type"`
	Fence             int64   `json:"fence"`
	Approval          *string `json:"approval_id,omitempty"`
	InstructionDigest *string `json:"instruction_digest,omitempty"`
}

func readGitHubRepairHandoff(ctx context.Context, db *sql.DB, token, tenant, task string) (githubRepairHandoff, error) {
	var out githubRepairHandoff
	digest, err := digestSession(token)
	if err != nil || db == nil || !resourceUUID.MatchString(tenant) || !resourceUUID.MatchString(task) {
		return out, errGitHubAuthorization
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err = db.QueryRowContext(ctx, "SELECT * FROM browseai.read_github_repair_handoff($1,$2,$3)", digest[:], tenant, task).Scan(
		&out.Grant, &out.Failure, &out.Revision, &out.StageIndex, &out.HeadSHA, &out.WorkflowID,
		&out.Event, &out.Fence, &out.Approval, &out.InstructionDigest)
	if err != nil || ctx.Err() != nil {
		return githubRepairHandoff{}, errGitHubAuthorization
	}
	return out, nil
}

func githubRepairHandoffRoute(a *authHTTP, db *sql.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if !a.sameOrigin(r) {
			resourceError(w, errForbidden)
			return
		}
		token := requestToken(r)
		p, err := a.sessions.authenticate(r.Context(), token)
		if err != nil {
			resourceError(w, err)
			return
		}
		if p.role != rolePlatformAdmin {
			resourceError(w, errForbidden)
			return
		}
		if !resourceUUID.MatchString(r.PathValue("id")) || r.URL.RawQuery != "" {
			resourceError(w, errResourceInvalid)
			return
		}
		out, err := readGitHubRepairHandoff(r.Context(), db, token, p.tenantID, r.PathValue("id"))
		if err != nil {
			resourceError(w, errResourceConflict)
			return
		}
		writeJSON(w, out)
	}
}
