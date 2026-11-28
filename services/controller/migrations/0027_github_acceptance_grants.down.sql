BEGIN;
DROP FUNCTION browseai.accept_authorized_github_ci(uuid,uuid,uuid,bigint,uuid,uuid,bytea,uuid,bytea,bigint,text);
DROP FUNCTION browseai.revoke_github_ci_acceptance_grant(uuid,uuid);
DROP FUNCTION browseai.issue_github_ci_acceptance_grant(uuid,uuid,uuid,bigint,uuid,uuid,bytea,uuid,bytea,bigint,text);
DROP TABLE browseai.github_ci_acceptance_grants;
REVOKE USAGE ON SCHEMA browseai FROM browseai_ci_acceptor,browseai_ci_approval_operator;
COMMIT;
