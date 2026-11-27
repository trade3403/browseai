BEGIN;
DROP FUNCTION browseai.revoke_session(bytea);
DROP FUNCTION browseai.resolve_session(bytea);
DROP FUNCTION browseai.issue_session(uuid,uuid,bytea);
REVOKE USAGE ON SCHEMA browseai FROM browseai_auth;
COMMIT;
