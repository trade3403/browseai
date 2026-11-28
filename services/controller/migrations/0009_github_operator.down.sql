BEGIN;
DROP FUNCTION browseai.provision_github_binding(uuid,bigint,bigint,text,bigint,bigint);
DROP TABLE browseai.github_binding_audit;
REVOKE USAGE ON SCHEMA browseai FROM browseai_github_operator;
COMMIT;
