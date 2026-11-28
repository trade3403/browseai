BEGIN;
-- Provisioned by a trusted operator after independent GitHub ownership
-- verification. No client may bind itself to an arbitrary installation ID.
CREATE TABLE browseai.github_installations (
    installation_id bigint PRIMARY KEY CHECK (installation_id>0),
    tenant_id uuid NOT NULL REFERENCES browseai.tenants(id),
    enabled boolean NOT NULL DEFAULT true,
    UNIQUE (tenant_id,installation_id)
);
CREATE TABLE browseai.github_repositories (
    tenant_id uuid NOT NULL,
    installation_id bigint NOT NULL,
    repository_id bigint NOT NULL CHECK (repository_id>0),
    full_name text NOT NULL CHECK (length(full_name) BETWEEN 3 AND 200),
    enabled boolean NOT NULL DEFAULT true,
    PRIMARY KEY (tenant_id,repository_id),
    FOREIGN KEY (tenant_id,installation_id) REFERENCES browseai.github_installations(tenant_id,installation_id)
);
ALTER TABLE browseai.github_installations ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.github_installations FORCE ROW LEVEL SECURITY;
ALTER TABLE browseai.github_repositories ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.github_repositories FORCE ROW LEVEL SECURITY;
CREATE POLICY installation_scope ON browseai.github_installations FOR SELECT TO browseai_app USING (tenant_id=browseai.current_tenant());
CREATE POLICY repository_scope ON browseai.github_repositories FOR SELECT TO browseai_app USING (tenant_id=browseai.current_tenant());
GRANT SELECT ON browseai.github_installations,browseai.github_repositories TO browseai_app;
COMMIT;
