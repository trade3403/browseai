BEGIN;
DROP FUNCTION browseai.approve_github_ci_repair(uuid,uuid,bigint,uuid,uuid,uuid,bigint,bytea);
DROP TRIGGER github_ci_repair_audit_trigger ON browseai.github_ci_failures;
DROP FUNCTION browseai.audit_github_ci_repair();
DROP TABLE browseai.github_ci_repair_approvals,browseai.github_ci_repair_audit;
COMMIT;
