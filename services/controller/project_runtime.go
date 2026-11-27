package main

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/url"
	"time"
)

func projectDatabaseReady(ctx context.Context, db *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT current_user='browseai_app'
AND NOT r.rolsuper AND NOT r.rolbypassrls
AND has_table_privilege(current_user,'browseai.projects','SELECT,INSERT,UPDATE,DELETE')
AND has_table_privilege(current_user,'browseai.tasks','SELECT,INSERT,UPDATE,DELETE')
AND has_table_privilege(current_user,'browseai.stages','SELECT,INSERT,UPDATE,DELETE')
AND has_table_privilege(current_user,'browseai.leases','SELECT,INSERT,UPDATE,DELETE')
AND has_sequence_privilege(current_user,'browseai.lease_fence','USAGE')
AND has_table_privilege(current_user,'browseai.idempotency_keys','SELECT')
AND has_table_privilege(current_user,'browseai.idempotency_keys','INSERT')
AND has_table_privilege(current_user,'browseai.idempotency_keys','UPDATE')
AND has_table_privilege(current_user,'browseai.audit_logs','SELECT')
AND has_table_privilege(current_user,'browseai.audit_logs','INSERT')
AND NOT has_table_privilege(current_user,'browseai.audit_logs','UPDATE,DELETE,TRUNCATE')
AND has_table_privilege(current_user,'browseai.task_approvals','SELECT')
AND has_table_privilege(current_user,'browseai.task_approvals','INSERT')
AND NOT has_table_privilege(current_user,'browseai.task_approvals','UPDATE,DELETE,TRUNCATE')
AND has_table_privilege(current_user,'browseai.events','SELECT')
AND has_table_privilege(current_user,'browseai.events','INSERT')
AND NOT has_table_privilege(current_user,'browseai.events','UPDATE,DELETE,TRUNCATE')
AND NOT has_table_privilege(current_user,'browseai.users','SELECT')
AND NOT has_table_privilege(current_user,'browseai.auth_sessions','SELECT')
AND NOT has_function_privilege(current_user,'browseai.issue_session(uuid,uuid,bytea)','EXECUTE')
AND (SELECT count(*) FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='browseai' AND c.relname IN ('projects','tasks','stages','leases','idempotency_keys','audit_logs','events','task_approvals') AND c.relrowsecurity AND c.relforcerowsecurity AND c.relowner<>r.oid)=8
FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`).Scan(&ok)
	if err != nil || !ok {
		return errors.New("project database role or schema invalid")
	}
	return nil
}

func openProjectRuntime(ctx context.Context, server *http.Server, authDB *sql.DB, dsn, origin string) (*sql.DB, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || u.User == nil || u.User.Username() != "browseai_app" || u.Query().Get("sslmode") != "verify-full" {
		return nil, errors.New("project DSN requires browseai_app and sslmode=verify-full")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, errors.New("invalid project database configuration")
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxLifetime(15 * time.Minute)
	if err := projectDatabaseReady(ctx, db); err != nil {
		db.Close()
		return nil, err
	}
	a, err := newAuthHTTP(origin, sqlCredentialVerifier{authDB}, sessionService{store: sqlSessionStore{authDB}})
	if err != nil {
		db.Close()
		return nil, err
	}
	server.Handler = projectRoutes(a, authDB, db)
	return db, nil
}

func projectRoutes(a *authHTTP, authDB, appDB *sql.DB) http.Handler {
	mux := http.NewServeMux()
	h := projectHTTP{auth: a, store: projectStore{db: appDB}}.handler()
	mux.Handle("/api/projects", h)
	mux.Handle("/api/projects/", h)
	tasks := taskHTTP{auth: a, store: taskStore{db: appDB}}.handler()
	mux.Handle("/api/tasks", tasks)
	mux.Handle("/api/tasks/", tasks)
	mux.Handle("/", authenticatedRoutes(a, func(ctx context.Context) error {
		if err := authDatabaseReady(ctx, authDB); err != nil {
			return err
		}
		return projectDatabaseReady(ctx, appDB)
	}))
	return mux
}
