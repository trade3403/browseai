BEGIN;
DROP FUNCTION browseai.consume_github_ci_evidence(uuid,uuid,bigint,uuid,bytea);
-- Destructive to consumption history: disposable CI/approved rollback only.
ALTER TABLE browseai.github_ci_evidence DROP COLUMN consumed_at;
COMMIT;
