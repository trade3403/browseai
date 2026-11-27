package main

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	_ "github.com/lib/pq"
)

func authenticatedRoutes(a *authHTTP, ready func(context.Context) error) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/api/auth/", a.handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]string{"status": "ok", "mode": "authenticated"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := ready(r.Context()); err != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, map[string]string{"status": "ok", "mode": "authenticated"})
	})
	// No mock projects or unauthenticated project endpoints in this mode.
	return mux
}

func validateAuthRuntime(addr, dsn string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("authenticated controller requires loopback listener behind trusted HTTPS proxy")
	}
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.User == nil || u.User.Username() != "browseai_auth" || u.Query().Get("sslmode") != "verify-full" {
		return errors.New("auth DSN requires browseai_auth and sslmode=verify-full")
	}
	return nil
}

func authDatabaseReady(ctx context.Context, db *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT current_user='browseai_auth'
AND NOT r.rolsuper AND NOT r.rolbypassrls
AND has_function_privilege(current_user,'browseai.login_candidate(text)','EXECUTE')
AND has_function_privilege(current_user,'browseai.resolve_session(bytea)','EXECUTE')
AND has_function_privilege(current_user,'browseai.issue_session(uuid,uuid,bytea)','EXECUTE')
AND has_function_privilege(current_user,'browseai.issue_verified_session(uuid,uuid,bytea,text)','EXECUTE')
AND NOT has_function_privilege(current_user,'browseai.provision_account(text,text,text)','EXECUTE')
AND NOT has_function_privilege(current_user,'browseai.reset_account_password(text,text)','EXECUTE')
AND NOT has_function_privilege(current_user,'browseai.set_account_enabled(text,boolean)','EXECUTE')
AND has_function_privilege(current_user,'browseai.revoke_session(bytea)','EXECUTE')
AND NOT has_table_privilege(current_user,'browseai.users','SELECT')
AND NOT has_table_privilege(current_user,'browseai.projects','SELECT')
FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`).Scan(&ok)
	if err != nil || !ok {
		return errors.New("authentication database role or schema invalid")
	}
	return nil
}

func openAuthRuntime(ctx context.Context, server *http.Server, dsn, origin string) (*sql.DB, error) {
	if err := validateAuthRuntime(server.Addr, dsn); err != nil {
		return nil, err
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, errors.New("invalid authentication database configuration")
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(15 * time.Minute)
	if err := authDatabaseReady(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	a, err := newAuthHTTP(origin, sqlCredentialVerifier{db}, sessionService{store: sqlSessionStore{db}})
	if err != nil {
		db.Close()
		return nil, err
	}
	server.Handler = authenticatedRoutes(a, func(ctx context.Context) error {
		return authDatabaseReady(ctx, db)
	})
	return db, nil
}
