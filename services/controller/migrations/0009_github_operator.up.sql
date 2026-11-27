BEGIN;
-- Dedicated offline operator. Provision the NOLOGIN group separately;
-- production login membership must never be granted to runtime roles.
GRANT USAGE ON SCHEMA browseai TO browseai_github_operator;
CREATE TABLE browseai.github_binding_audit (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    operator_role text NOT NULL DEFAULT session_user,
    tenant_id uuid NOT NULL,
    installation_id bigint NOT NULL CHECK (installation_id>0),
    repository_id bigint NOT NULL CHECK (repository_id>0),
    app_id bigint NOT NULL CHECK (app_id>0),
    account_id bigint NOT NULL CHECK (account_id>0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
REVOKE ALL ON browseai.github_binding_audit FROM PUBLIC;
-- Use an append-only narrow SQL function rather than granting table writes.
-- The function owner is the trusted migration role, which must be able to
-- access forced-RLS tables (explicit owner policies or BYPASSRLS).
CREATE FUNCTION browseai.provision_github_binding(p_tenant uuid,p_installation bigint,p_repository bigint,p_name text,p_app bigint,p_account bigint)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path=pg_catalog,pg_temp
AS $$
BEGIN
    IF p_tenant IS NULL OR p_installation IS NULL OR p_installation<=0 OR p_repository IS NULL OR p_repository<=0
       OR p_app IS NULL OR p_app<=0 OR p_account IS NULL OR p_account<=0
       OR p_name IS NULL OR p_name !~ '^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$' OR length(p_name)>200 THEN
        RAISE EXCEPTION 'invalid binding' USING ERRCODE='22023';
    END IF;
    PERFORM 1 FROM browseai.tenants WHERE id=p_tenant AND enabled FOR SHARE;
    IF NOT FOUND THEN RAISE EXCEPTION 'binding unavailable'; END IF;
    INSERT INTO browseai.github_installations(installation_id,tenant_id)
    VALUES(p_installation,p_tenant) ON CONFLICT(installation_id) DO NOTHING;
    PERFORM 1 FROM browseai.github_installations
    WHERE installation_id=p_installation AND tenant_id=p_tenant AND enabled FOR UPDATE;
    IF NOT FOUND THEN RAISE EXCEPTION 'binding unavailable'; END IF;
    INSERT INTO browseai.github_repositories(tenant_id,installation_id,repository_id,full_name)
    VALUES(p_tenant,p_installation,p_repository,p_name) ON CONFLICT(tenant_id,repository_id) DO NOTHING;
    IF FOUND THEN
        INSERT INTO browseai.github_binding_audit(tenant_id,installation_id,repository_id,app_id,account_id)
        VALUES(p_tenant,p_installation,p_repository,p_app,p_account);
    ELSE
        PERFORM 1 FROM browseai.github_repositories
        WHERE tenant_id=p_tenant AND installation_id=p_installation AND repository_id=p_repository
          AND lower(full_name)=lower(p_name) AND enabled FOR UPDATE;
        IF NOT FOUND THEN RAISE EXCEPTION 'binding unavailable'; END IF;
        -- Legacy rows without a matching audited identity require review.
        PERFORM 1 FROM browseai.github_binding_audit WHERE tenant_id=p_tenant
          AND installation_id=p_installation AND repository_id=p_repository
          AND app_id=p_app AND account_id=p_account;
        IF NOT FOUND THEN RAISE EXCEPTION 'binding approval unavailable'; END IF;
    END IF;
    RETURN true;
END
$$;
REVOKE ALL ON FUNCTION browseai.provision_github_binding(uuid,bigint,bigint,text,bigint,bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.provision_github_binding(uuid,bigint,bigint,text,bigint,bigint) TO browseai_github_operator;
COMMIT;
