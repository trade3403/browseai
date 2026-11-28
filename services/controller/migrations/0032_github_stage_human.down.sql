BEGIN;
DROP FUNCTION browseai.review_authorized_github_stage(bytea,uuid,uuid,bigint,uuid,uuid,text);
REVOKE USAGE ON SCHEMA browseai FROM browseai_stage_human;
COMMIT;
