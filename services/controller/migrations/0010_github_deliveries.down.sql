BEGIN;
DROP FUNCTION browseai.record_github_delivery(uuid,bytea,bigint,bigint,text);
DROP TABLE browseai.github_deliveries;
REVOKE USAGE ON SCHEMA browseai FROM browseai_webhook;
COMMIT;
