BEGIN;
DROP FUNCTION browseai.reserve_github_ci_repair(uuid,uuid,bigint,uuid,bigint);
DROP FUNCTION browseai.record_github_ci_failure(uuid,uuid,bigint,uuid,bigint,bigint,text,text,bigint,text,text,bigint,bigint,bigint[],timestamptz);
DROP TABLE browseai.github_ci_failures;
COMMIT;
