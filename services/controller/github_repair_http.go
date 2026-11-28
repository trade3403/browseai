package main

import (
	"context"
	"database/sql"
	"net/http"
	"time"
)

func githubRepairHumanReady(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errGitHubAuthorization
	}
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT current_user='browseai_repair_human' AND session_user=current_user
AND NOT r.rolsuper AND NOT r.rolbypassrls AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
AND has_function_privilege(current_user,'browseai.review_authorized_github_repair(bytea,uuid,uuid,bigint,uuid,uuid,uuid,uuid,bigint,bytea,text)','EXECUTE')
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
WHERE n.nspname='browseai' AND p.proname<>'review_authorized_github_repair'
AND has_function_privilege(current_user,p.oid,'EXECUTE'))
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='browseai' AND c.relkind IN ('r','p','v','m','f')
AND (has_table_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
OR has_any_column_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,REFERENCES')))
FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`).Scan(&ok)
	if err != nil || !ok {
		return errGitHubAuthorization
	}
	return nil
}

type githubRepairReviewRequest struct {
	Revision     int64  `json:"revision"`
	Fence        int64  `json:"fence"`
	Grant        string `json:"grant_id"`
	Failure      string `json:"failure_id"`
	Approval     string `json:"approval_id"`
	Confirmation string `json:"confirmation_id,omitempty"`
	Instruction  []byte `json:"instruction_base64"`
	Reviewed     bool   `json:"reviewed"`
	ManualSent   bool   `json:"manual_sent"`
}

func (in githubRepairReviewRequest) valid(action string) bool {
	if in.Revision < 2 || in.Revision == 1<<63-1 || in.Fence <= 0 ||
		!resourceUUID.MatchString(in.Grant) || !resourceUUID.MatchString(in.Failure) || !resourceUUID.MatchString(in.Approval) ||
		!validPlanText(string(in.Instruction), 16384) || !in.Reviewed {
		return false
	}
	switch action {
	case "approve":
		return in.Confirmation == "" && !in.ManualSent
	case "confirm_sent":
		return in.ManualSent && resourceUUID.MatchString(in.Confirmation) && in.Confirmation != in.Approval
	default:
		return false
	}
}

func githubRepairReviewRoutes(a *authHTTP, db *sql.DB, fallback http.Handler) http.Handler {
	mux := http.NewServeMux()
	review := func(action string) http.HandlerFunc {
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
			var in githubRepairReviewRequest
			if !resourceUUID.MatchString(task) || !resourceUUID.MatchString(p.tenantID) || r.URL.RawQuery != "" {
				resourceError(w, errResourceInvalid)
				return
			}
			if !decodeResourceLimit(w, r, &in, 32768) {
				return
			}
			if !in.valid(action) {
				resourceError(w, errResourceInvalid)
				return
			}
			// The SQL wrapper resolves the session again while locking the
			// mutation. Approval/confirmation UUIDs and revision reject
			// replay; a conflict is not permission to resend a message.
			if err := reviewAuthorizedGitHubRepair(r.Context(), db, token, p.tenantID, task, in.Grant, in.Failure, in.Approval, in.Confirmation, in.Revision, in.Fence, in.Instruction, action, in.ManualSent); err != nil {
				resourceError(w, errResourceConflict)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}
	mux.HandleFunc("POST /api/tasks/{id}/github-repair/approve", review("approve"))
	mux.HandleFunc("POST /api/tasks/{id}/github-repair/confirm-sent", review("confirm_sent"))
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := githubRepairHumanReady(ctx, db); err != nil {
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
func openGitHubRepairReviewRuntime(ctx context.Context, server *http.Server, authDB *sql.DB, dsn, origin string) (*sql.DB, error) {
	if server == nil || server.Handler == nil || authDB == nil || !githubRoleDSNValid(dsn, "browseai_repair_human") {
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
	if err := githubRepairHumanReady(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	server.Handler = githubRepairReviewRoutes(a, db, server.Handler)
	return db, nil
}
