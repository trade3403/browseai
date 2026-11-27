BEGIN;
DROP FUNCTION browseai.publish_github_monitor(uuid,uuid,text,integer,timestamptz);
DROP FUNCTION browseai.claim_github_monitor();
DROP FUNCTION browseai.enqueue_github_monitor(uuid,uuid,bigint,text);
DROP TABLE browseai.github_monitors;
REVOKE USAGE ON SCHEMA browseai FROM browseai_monitor;
COMMIT;
