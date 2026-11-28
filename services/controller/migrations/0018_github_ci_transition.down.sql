BEGIN;
DROP FUNCTION browseai.accept_github_ci(uuid,uuid,bigint,uuid,uuid,uuid,bytea,uuid,bytea,bigint,text);
-- Destructive to acceptance linkage; only approved rollback/disposable CI.
DROP TABLE browseai.github_ci_acceptance;
COMMIT;
