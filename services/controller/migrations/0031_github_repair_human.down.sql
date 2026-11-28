BEGIN;
DROP FUNCTION browseai.review_authorized_github_repair(bytea,uuid,uuid,bigint,uuid,uuid,uuid,uuid,bigint,bytea,text);
REVOKE USAGE ON SCHEMA browseai FROM browseai_repair_human;
COMMIT;
