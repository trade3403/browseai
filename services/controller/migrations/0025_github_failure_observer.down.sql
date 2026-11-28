BEGIN;
DROP FUNCTION browseai.record_authorized_github_failure(uuid,uuid,uuid,bigint,uuid,bigint,bigint,text,text,bigint,text,text,bigint,bigint,bigint[],timestamptz);
DROP TABLE browseai.github_failure_observer_grants;
REVOKE USAGE ON SCHEMA browseai FROM browseai_failure_observer;
COMMIT;
