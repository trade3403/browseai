package main

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

type githubStageReviewRequest struct {
	Revision   int64  `json:"revision"`
	Acceptance string `json:"acceptance_id"`
	Decision   string `json:"decision_id"`
	Reviewed   bool   `json:"reviewed"`
}

func (in githubStageReviewRequest) valid(action taskAction) bool {
	return in.Revision >= 2 && in.Revision < 1<<63-1 && in.Reviewed &&
		resourceUUID.MatchString(in.Acceptance) && resourceUUID.MatchString(in.Decision) &&
		in.Acceptance != in.Decision && (action == actionNext || action == actionComplete)
}

func githubStageReviewRoutes(a *authHTTP, db *sql.DB, fallback http.Handler) http.Handler {
	mux := http.NewServeMux()
	review := func(action taskAction) http.HandlerFunc {
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
			task := r.PathValue("id")
			var in githubStageReviewRequest
			if !resourceUUID.MatchString(task) || !resourceUUID.MatchString(p.tenantID) || r.URL.RawQuery != "" {
				resourceError(w, errResourceInvalid)
				return
			}
			if !decodeResourceLimit(w, r, &in, 4096) {
				return
			}
			if !in.valid(action) {
				resourceError(w, errResourceInvalid)
				return
			}
			// Resolve the authenticated session again under the database mutation lock.
			if err := reviewAuthorizedGitHubStage(r.Context(), db, token, p.tenantID, task, in.Acceptance, in.Decision, in.Revision, action, in.Reviewed); err != nil {
				resourceError(w, errResourceConflict)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}
	mux.HandleFunc("POST /api/tasks/{id}/github-stage/next", review(actionNext))
	mux.HandleFunc("POST /api/tasks/{id}/github-stage/complete", review(actionComplete))
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := githubStageHumanReady(ctx, db); err != nil {
			resourceError(w, err)
			return
		}
		fallback.ServeHTTP(w, r)
	})
	mux.Handle("/", fallback)
	return mux
}

// Opt-in only in authenticated controller mode. No owner credential or
// ordinary task pool may serve the review routes.
func openGitHubStageReviewRuntime(ctx context.Context, server *http.Server, authDB *sql.DB, dsn, origin string) (*sql.DB, error) {
	if server == nil || server.Handler == nil || authDB == nil || !githubRoleDSNValid(dsn, "browseai_stage_human") {
		return nil, errGitHubAuthorization
	}
	a, err := newAuthHTTP(origin, sqlCredentialVerifier{authDB}, sessionService{store: sqlSessionStore{authDB}})
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, errGitHubAuthorization
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(15 * time.Minute)
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := githubStageHumanReady(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	server.Handler = githubStageReviewRoutes(a, db, server.Handler)
	return db, nil
}
