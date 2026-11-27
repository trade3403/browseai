-- Run as the migration owner, never as the application role.
-- Provision browseai_app as NOSUPERUSER NOBYPASSRLS without owner membership first.
BEGIN;
CREATE SCHEMA browseai;
REVOKE ALL ON SCHEMA browseai FROM PUBLIC;
GRANT USAGE ON SCHEMA browseai TO browseai_app;

CREATE TABLE browseai.tenants (
    id uuid PRIMARY KEY,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE browseai.users (
    id uuid PRIMARY KEY,
    login text NOT NULL UNIQUE CHECK (length(login) BETWEEN 1 AND 254),
    password_hash text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE browseai.memberships (
    tenant_id uuid NOT NULL REFERENCES browseai.tenants(id),
    user_id uuid NOT NULL REFERENCES browseai.users(id),
    role text NOT NULL CHECK (role IN ('user', 'platform_admin')),
    PRIMARY KEY (tenant_id, user_id)
);
CREATE TABLE browseai.auth_sessions (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    user_id uuid NOT NULL,
    token_digest bytea NOT NULL UNIQUE CHECK (octet_length(token_digest) = 32),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, user_id) REFERENCES browseai.memberships(tenant_id, user_id),
    CHECK (expires_at > created_at)
);

CREATE TABLE browseai.projects (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL REFERENCES browseai.tenants(id),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    repository_id bigint NOT NULL CHECK (repository_id > 0),
    repository text NOT NULL CHECK (length(repository) BETWEEN 3 AND 200),
    code_branch text NOT NULL CHECK (length(code_branch) BETWEEN 1 AND 255),
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, id, repository_id, code_branch)
);
CREATE TABLE browseai.tasks (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    project_id uuid NOT NULL,
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 128),
    state text NOT NULL DEFAULT 'QUEUED' CHECK (state IN (
        'QUEUED','READY','AWAIT_APPROVAL','DEVELOPING','WAIT_CI','CI_FAILED',
        'STAGE_PASSED','HANDOFF_REQUIRED','HANDOFF_READY','PAUSED','OFFLINE',
        'NEED_HUMAN','COMPLETED','STOPPED'
    )),
    suspended_state text CHECK (suspended_state IN (
        'QUEUED','READY','AWAIT_APPROVAL','DEVELOPING','WAIT_CI','CI_FAILED','STAGE_PASSED'
    )),
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
    stage_index integer NOT NULL DEFAULT 0,
    stage_count integer NOT NULL CHECK (stage_count BETWEEN 1 AND 100),
    repairs integer NOT NULL DEFAULT 0,
    max_repairs integer NOT NULL DEFAULT 5 CHECK (max_repairs BETWEEN 0 AND 20),
    code_sha text CHECK (code_sha ~ '^[0-9a-f]{40}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (tenant_id, project_id) REFERENCES browseai.projects(tenant_id, id),
    UNIQUE (tenant_id, id),
    UNIQUE (tenant_id, id, project_id),
    CHECK (stage_index >= 0 AND stage_index < stage_count),
    CHECK (repairs >= 0 AND repairs <= max_repairs),
    CHECK (state NOT IN ('WAIT_CI','STAGE_PASSED','COMPLETED') OR code_sha IS NOT NULL),
    CHECK (state <> 'COMPLETED' OR stage_index = stage_count - 1),
    CHECK ((state IN ('PAUSED','OFFLINE','NEED_HUMAN','HANDOFF_REQUIRED','HANDOFF_READY')) = (suspended_state IS NOT NULL))
);
CREATE INDEX tasks_tenant_project_idx ON browseai.tasks(tenant_id, project_id);
CREATE TABLE browseai.stages (
    tenant_id uuid NOT NULL,
    task_id uuid NOT NULL,
    stage_id text NOT NULL CHECK (length(stage_id) BETWEEN 1 AND 64),
    ordinal integer NOT NULL CHECK (ordinal BETWEEN 0 AND 99),
    weight integer NOT NULL CHECK (weight BETWEEN 1 AND 10000),
    PRIMARY KEY (tenant_id, task_id, stage_id),
    UNIQUE (tenant_id, task_id, ordinal),
    FOREIGN KEY (tenant_id, task_id) REFERENCES browseai.tasks(tenant_id, id)
);

CREATE SEQUENCE browseai.lease_fence AS bigint NO CYCLE;
CREATE TABLE browseai.leases (
    repository_id bigint NOT NULL,
    code_branch text NOT NULL,
    tenant_id uuid NOT NULL,
    task_id uuid NOT NULL,
    project_id uuid NOT NULL,
    fence bigint NOT NULL DEFAULT nextval('browseai.lease_fence') CHECK (fence > 0),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (repository_id, code_branch),
    UNIQUE (fence),
    FOREIGN KEY (tenant_id, task_id, project_id) REFERENCES browseai.tasks(tenant_id, id, project_id),
    FOREIGN KEY (tenant_id, project_id, repository_id, code_branch)
        REFERENCES browseai.projects(tenant_id, id, repository_id, code_branch)
);
-- A global key remains occupied until trusted reconciliation releases it.
-- An expired timestamp alone does not permit a second writer.

-- Transaction-local tenant context must be set by the authenticated service.
-- Missing/empty tenant fails closed; malformed UUID raises an error.
CREATE FUNCTION browseai.current_tenant() RETURNS uuid
LANGUAGE sql STABLE SECURITY INVOKER
SET search_path = pg_catalog
AS $$ SELECT nullif(current_setting('browseai.tenant_id', true), '')::uuid $$;
REVOKE ALL ON FUNCTION browseai.current_tenant() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.current_tenant() TO browseai_app;

DO $$
DECLARE t text;
BEGIN
    FOREACH t IN ARRAY ARRAY['projects','tasks','stages','leases'] LOOP
        EXECUTE format('ALTER TABLE browseai.%I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE browseai.%I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format('CREATE POLICY tenant_scope ON browseai.%I FOR ALL TO browseai_app USING (tenant_id = browseai.current_tenant()) WITH CHECK (tenant_id = browseai.current_tenant())', t);
        EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON browseai.%I TO browseai_app', t);
    END LOOP;
END $$;
GRANT USAGE ON SEQUENCE browseai.lease_fence TO browseai_app;
-- No app grants to identity/session tables; separate authentication privileges
-- and application-level authorization are a subsequent batch.
COMMIT;
