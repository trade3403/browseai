BEGIN;
CREATE TABLE browseai.task_approvals (
    tenant_id uuid NOT NULL,
    task_id uuid NOT NULL,
    revision bigint NOT NULL CHECK (revision >= 0),
    stage_index integer NOT NULL CHECK (stage_index BETWEEN 0 AND 99),
    fence bigint NOT NULL CHECK (fence > 0),
    actor_id uuid NOT NULL,
    approved_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (tenant_id,task_id,revision),
    FOREIGN KEY (tenant_id,task_id) REFERENCES browseai.tasks(tenant_id,id)
);
ALTER TABLE browseai.task_approvals ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.task_approvals FORCE ROW LEVEL SECURITY;
CREATE POLICY approval_read ON browseai.task_approvals FOR SELECT TO browseai_app
USING (tenant_id=browseai.current_tenant());
CREATE POLICY approval_append ON browseai.task_approvals FOR INSERT TO browseai_app
WITH CHECK (tenant_id=browseai.current_tenant() AND actor_id=nullif(current_setting('browseai.actor_id',true),'')::uuid);
GRANT SELECT,INSERT ON browseai.task_approvals TO browseai_app;
COMMIT;
