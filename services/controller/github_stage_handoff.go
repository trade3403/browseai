package main

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

type githubStageHandoff struct {
	Acceptance   string `json:"acceptance_id"`
	Revision     int64  `json:"revision"`
	StageIndex   int    `json:"stage_index"`
	StageCount   int    `json:"stage_count"`
	HeadSHA      string `json:"head_sha"`
	WorkflowID   int64  `json:"workflow_id"`
	PushID       string `json:"push_id"`
	PRID         string `json:"pr_id"`
	PushDigest   string `json:"push_digest"`
	PRDigest     string `json:"pr_digest"`
	SourceDigest string `json:"source_digest"`
}

func readGitHubStageHandoff(ctx context.Context, db *sql.DB, token, tenant, task string) (githubStageHandoff, error) {
	var out githubStageHandoff
	digest, err := digestSession(token)
	if err != nil || db == nil || !resourceUUID.MatchString(tenant) || !resourceUUID.MatchString(task) {
		return out, errGitHubAuthorization
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	err = db.QueryRowContext(ctx, "SELECT * FROM browseai.read_github_stage_handoff($1,$2,$3)", digest[:], tenant, task).Scan(
		&out.Acceptance, &out.Revision, &out.StageIndex, &out.StageCount, &out.HeadSHA, &out.WorkflowID,
		&out.PushID, &out.PRID, &out.PushDigest, &out.PRDigest, &out.SourceDigest)
	if err != nil || ctx.Err() != nil {
		return githubStageHandoff{}, errGitHubAuthorization
	}
	return out, nil
}

func githubStageHandoffRoute(a *authHTTP, db *sql.DB) http.HandlerFunc {
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
		out, err := readGitHubStageHandoff(r.Context(), db, token, p.tenantID, r.PathValue("id"))
		if err != nil {
			resourceError(w, errResourceConflict)
			return
		}
		writeJSON(w, out)
	}
}
