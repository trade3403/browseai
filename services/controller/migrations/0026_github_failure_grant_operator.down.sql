BEGIN;
DROP FUNCTION browseai.revoke_github_failure_grant(uuid,uuid);
DROP FUNCTION browseai.issue_github_failure_grant(uuid,uuid,uuid,uuid,bigint,bigint,bigint,text,text,bigint,text,text,timestamptz);
ALTER TABLE browseai.github_failure_observer_grants DROP COLUMN revoked_by;
REVOKE USAGE ON SCHEMA browseai FROM browseai_failure_grant_operator;
COMMIT;
