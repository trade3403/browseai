BEGIN;
GRANT USAGE ON SCHEMA browseai TO browseai_webhook;
CREATE TABLE browseai.github_deliveries (
    delivery_id uuid PRIMARY KEY,
    body_digest bytea NOT NULL UNIQUE CHECK (octet_length(body_digest)=32),
    tenant_id uuid NOT NULL,
    installation_id bigint NOT NULL,
    repository_id bigint NOT NULL,
    event_type text NOT NULL CHECK (event_type IN ('workflow_run','check_run','check_suite','push','pull_request')),
    received_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY (tenant_id,installation_id) REFERENCES browseai.github_installations(tenant_id,installation_id),
    FOREIGN KEY (tenant_id,repository_id) REFERENCES browseai.github_repositories(tenant_id,repository_id)
);
REVOKE ALL ON browseai.github_deliveries FROM PUBLIC;
-- Only the dedicated verified-webhook receiver may call this function.
-- Never trust caller-supplied tenant identity or treat a delivery as CI evidence.
CREATE FUNCTION browseai.record_github_delivery(p_delivery uuid,p_digest bytea,p_installation bigint,p_repository bigint,p_event text)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path=pg_catalog,pg_temp
AS $$
DECLARE tid uuid;
BEGIN
    IF p_delivery IS NULL OR p_digest IS NULL OR octet_length(p_digest)<>32
       OR p_installation IS NULL OR p_installation<=0 OR p_repository IS NULL OR p_repository<=0
       OR p_event IS NULL OR p_event NOT IN ('workflow_run','check_run','check_suite','push','pull_request') THEN
        RETURN 'rejected';
    END IF;
    SELECT i.tenant_id INTO tid FROM browseai.github_installations i
    JOIN browseai.github_repositories r ON r.tenant_id=i.tenant_id AND r.installation_id=i.installation_id
    JOIN browseai.tenants t ON t.id=i.tenant_id
    WHERE i.installation_id=p_installation AND r.repository_id=p_repository
      AND i.enabled AND r.enabled AND t.enabled
      AND EXISTS (SELECT 1 FROM browseai.github_binding_audit a WHERE a.tenant_id=i.tenant_id
          AND a.installation_id=i.installation_id AND a.repository_id=r.repository_id)
    FOR SHARE OF i,r,t;
    IF NOT FOUND THEN RETURN 'rejected'; END IF;
    INSERT INTO browseai.github_deliveries(delivery_id,body_digest,tenant_id,installation_id,repository_id,event_type)
    VALUES(p_delivery,p_digest,tid,p_installation,p_repository,p_event)
    ON CONFLICT DO NOTHING;
    IF FOUND THEN RETURN 'accepted'; END IF;
    -- An identical body with a different unsigned header is a replay.
    IF EXISTS (SELECT 1 FROM browseai.github_deliveries d WHERE d.body_digest=p_digest
        AND d.tenant_id=tid AND d.installation_id=p_installation AND d.repository_id=p_repository) THEN
        RETURN 'duplicate';
    END IF;
    RETURN 'rejected';
END
$$;
REVOKE ALL ON FUNCTION browseai.record_github_delivery(uuid,bytea,bigint,bigint,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.record_github_delivery(uuid,bytea,bigint,bigint,text) TO browseai_webhook;
COMMIT;
