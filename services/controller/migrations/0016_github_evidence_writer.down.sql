BEGIN;
DROP FUNCTION browseai.insert_github_ci_evidence(bytea,bytea,timestamptz);
REVOKE USAGE ON SCHEMA browseai FROM browseai_evidence_writer;
COMMIT;
