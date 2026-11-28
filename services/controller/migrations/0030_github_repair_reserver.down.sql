BEGIN;
DROP FUNCTION browseai.reserve_authorized_github_repair(uuid,uuid,uuid,bigint,uuid,bigint);
REVOKE USAGE ON SCHEMA browseai FROM browseai_repair_reserver;
COMMIT;
