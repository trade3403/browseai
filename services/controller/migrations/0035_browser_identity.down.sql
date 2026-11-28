BEGIN;
DROP FUNCTION browseai.record_browser_heartbeat(uuid,bigint,bytea,bigint,integer,integer);
DROP FUNCTION browseai.manage_browser_identity(uuid,bigint,bytea,uuid,text);
DROP TABLE browseai.browser_node_audit,browseai.browser_nodes;
REVOKE USAGE ON SCHEMA browseai FROM browseai_browser_operator,browseai_browser_node;
COMMIT;
