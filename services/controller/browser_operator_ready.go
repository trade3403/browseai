package main

import (
	"context"
	"database/sql"
)

func browserOperatorReady(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errBrowserNode
	}
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT current_user='browseai_browser_operator' AND session_user=current_user
AND NOT r.rolsuper AND NOT r.rolbypassrls AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
AND has_function_privilege(current_user,'browseai.manage_browser_identity(uuid,bigint,bytea,uuid,text)','EXECUTE')
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
WHERE n.nspname='browseai' AND p.oid<>'browseai.manage_browser_identity(uuid,bigint,bytea,uuid,text)'::regprocedure
AND has_function_privilege(current_user,p.oid,'EXECUTE'))
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace
WHERE n.nspname='browseai' AND c.relkind IN ('r','p','v','m','f')
AND (has_table_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')
OR has_any_column_privilege(current_user,c.oid,'SELECT,INSERT,UPDATE,REFERENCES')))
FROM pg_catalog.pg_roles r WHERE r.rolname=current_user`).Scan(&ok)
	if err != nil || !ok {
		return errBrowserNode
	}
	return nil
}
