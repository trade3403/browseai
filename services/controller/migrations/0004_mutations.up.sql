BEGIN;
CREATE TABLE browseai.idempotency_keys (
    tenant_id uuid NOT NULL REFERENCES browseai.tenants(id),
    user_id uuid NOT NULL,
    request_key text NOT NULL CHECK (length(request_key) BETWEEN 1 AND 128),
    request_hash bytea NOT NULL CHECK (octet_length(request_hash)=32),
    result jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id,user_id,request_key)
);
CREATE TABLE browseai.audit_logs (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES browseai.tenants(id),
    actor_id uuid NOT NULL,
    action text NOT NULL CHECK (length(action) BETWEEN 1 AND 80),
    resource_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision >= 0),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE browseai.events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL REFERENCES browseai.tenants(id),
    audit_id uuid NOT NULL UNIQUE REFERENCES browseai.audit_logs(id),
    kind text NOT NULL CHECK (length(kind) BETWEEN 1 AND 80),
    resource_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision >= 0),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX events_tenant_created_idx ON browseai.events(tenant_id,created_at,id);
ALTER TABLE browseai.idempotency_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.idempotency_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY request_owner ON browseai.idempotency_keys TO browseai_app
USING (tenant_id=browseai.current_tenant() AND user_id=nullif(current_setting('browseai.actor_id',true),'')::uuid)
WITH CHECK (tenant_id=browseai.current_tenant() AND user_id=nullif(current_setting('browseai.actor_id',true),'')::uuid);
GRANT SELECT,INSERT,UPDATE ON browseai.idempotency_keys TO browseai_app;
ALTER TABLE browseai.audit_logs ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.audit_logs FORCE ROW LEVEL SECURITY;
CREATE POLICY audit_read ON browseai.audit_logs FOR SELECT TO browseai_app USING (tenant_id=browseai.current_tenant());
CREATE POLICY audit_append ON browseai.audit_logs FOR INSERT TO browseai_app
WITH CHECK (tenant_id=browseai.current_tenant() AND actor_id=nullif(current_setting('browseai.actor_id',true),'')::uuid);
GRANT SELECT,INSERT ON browseai.audit_logs TO browseai_app;
ALTER TABLE browseai.events ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.events FORCE ROW LEVEL SECURITY;
CREATE POLICY event_read ON browseai.events FOR SELECT TO browseai_app USING (tenant_id=browseai.current_tenant());
CREATE POLICY event_append ON browseai.events FOR INSERT TO browseai_app
WITH CHECK (tenant_id=browseai.current_tenant() AND EXISTS (
    SELECT 1 FROM browseai.audit_logs a WHERE a.id=audit_id AND a.tenant_id=events.tenant_id
    AND a.resource_id=events.resource_id AND a.action=events.kind AND a.revision=events.revision
));
GRANT SELECT,INSERT ON browseai.events TO browseai_app;
COMMIT;
