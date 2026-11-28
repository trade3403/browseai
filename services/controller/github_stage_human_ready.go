package main

import (
	"context"
	"database/sql"
)

func githubStageHumanReady(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return errGitHubAuthorization
	}
	var ok bool
	err := db.QueryRowContext(ctx, `SELECT current_user='browseai_stage_human' AND session_user=current_user
AND NOT r.rolsuper AND NOT r.rolbypassrls AND NOT r.rolcreaterole AND NOT r.rolcreatedb AND NOT r.rolreplication
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_auth_members WHERE member=r.oid)
AND has_function_privilege(current_user,'browseai.review_authorized_github_stage(bytea,uuid,uuid,bigint,uuid,uuid,text)','EXECUTE')
AND has_function_privilege(current_user,'browseai.read_github_stage_handoff(bytea,uuid,uuid)','EXECUTE')
AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_proc p JOIN pg_catalog.pg_namespace n ON n.oid=p.pronamespace
WHERE n.nspname='browseai' AND p.oid NOT IN ('browseai.review_authorized_github_stage(bytea,uuid,uuid,bigint,uuid,uuid,text)'::regprocedure,'browseai.read_github_stage_handoff(bytea,uuid,uuid)'::regprocedure)
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
