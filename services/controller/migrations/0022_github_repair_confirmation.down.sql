BEGIN;
DROP FUNCTION browseai.confirm_github_ci_repair(uuid,uuid,bigint,uuid,uuid,uuid,bigint,bytea);
DROP TABLE browseai.github_ci_repair_confirmations;
COMMIT;
