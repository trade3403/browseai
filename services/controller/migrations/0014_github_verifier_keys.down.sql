BEGIN;
DROP FUNCTION browseai.resolve_github_verifier(uuid,bigint,bigint,uuid,uuid,timestamptz);
DROP FUNCTION browseai.revoke_github_verifier(uuid,bigint,bigint,uuid,uuid);
DROP FUNCTION browseai.register_github_verifier(uuid,bigint,bigint,uuid,uuid,bytea,timestamptz,timestamptz,uuid);
DROP TABLE browseai.github_verifier_audit;
DROP TABLE browseai.github_verifier_keys;
REVOKE USAGE ON SCHEMA browseai FROM browseai_verifier_operator,browseai_verifier_reader;
COMMIT;
