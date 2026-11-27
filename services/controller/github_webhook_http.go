package main

import (
	"context"
	"database/sql"
	"net"
	"net/http"
	"time"
)

func loadGitHubWebhookSecret(path string) ([]byte, error) {
	raw, err := readProtectedGitHubFile(path, 4096)
	if err != nil || len(raw) < 32 {
		return nil, errGitHubAuthorization
	}
	// Exact bytes: no silent trimming of a configured secret.
	for _, b := range raw {
		if b < 33 || b > 126 {
			return nil, errGitHubAuthorization
		}
	}
	return raw, nil
}

func githubWebhookReady(ctx context.Context, db *sql.DB) error {
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT current_user='browseai_webhook'
AND session_user=current_user AND NOT r.rolsuper AND NOT r.rolbypassrls
AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication
AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
AND has_function_privilege(current_user,'browseai.record_github_delivery(uuid,bytea,bigint,bigint,text)','EXECUTE')
AND NOT has_function_privilege(current_user,'browseai.provision_github_binding(uuid,bigint,bigint,text,bigint,bigint)','EXECUTE')
AND NOT has_table_privilege(current_user,'browseai.github_deliveries','SELECT,INSERT,UPDATE,DELETE,TRUNCATE')
AND NOT has_table_privilege(current_user,'browseai.github_installations','SELECT,INSERT,UPDATE,DELETE,TRUNCATE')
AND NOT has_table_privilege(current_user,'browseai.github_repositories','SELECT,INSERT,UPDATE,DELETE,TRUNCATE')
AND NOT has_table_privilege(current_user,'browseai.users','SELECT,INSERT,UPDATE,DELETE,TRUNCATE')
AND NOT has_table_privilege(current_user,'browseai.projects','SELECT,INSERT,UPDATE,DELETE,TRUNCATE')
FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`).Scan(&ok)
	if err != nil || !ok {
		return errGitHubAuthorization
	}
	return nil
}

type githubDeliveryRecorder interface {
	record(context.Context, githubWebhookDelivery) (string, error)
}

func newGitHubWebhookHandler(secret []byte, store githubDeliveryRecorder) http.Handler {
	key := append([]byte(nil), secret...)
	slots := make(chan struct{}, 8)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != "/webhook/github" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			w.Header().Set("Retry-After", "5")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if r.ContentLength > githubWebhookMaxBody {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, githubWebhookMaxBody+1)
		d, err := verifyGitHubWebhook(r, key)
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		result, err := store.record(ctx, d)
		if err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		switch result {
		case "accepted", "duplicate", "rejected":
			// Do not expose tenant membership or delivery history.
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
}

func newGitHubWebhookServer(addr string, secret []byte, store githubDeliveryRecorder) (*http.Server, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() || len(secret) < 32 || len(secret) > 4096 || store == nil {
		return nil, errGitHubAuthorization
	}
	return &http.Server{
		Addr: addr, Handler: newGitHubWebhookHandler(secret, store),
		ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 15 * time.Second,
		MaxHeaderBytes: 16 * 1024,
	}, nil
}
