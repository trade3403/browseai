-- Provision browseai_auth separately, without elevated membership.
-- Migration owner must be separate from runtime roles.
BEGIN;
GRANT USAGE ON SCHEMA browseai TO browseai_auth;

CREATE FUNCTION browseai.issue_session(p_user uuid, p_tenant uuid, p_digest bytea)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
    WITH issued AS (
        INSERT INTO browseai.auth_sessions(id,tenant_id,user_id,token_digest,expires_at)
        SELECT gen_random_uuid(),m.tenant_id,m.user_id,p_digest,now()+interval '8 hours'
        FROM browseai.memberships m
        JOIN browseai.users u ON u.id=m.user_id AND u.enabled
        JOIN browseai.tenants t ON t.id=m.tenant_id AND t.enabled
        WHERE m.user_id=p_user AND m.tenant_id=p_tenant
          AND octet_length(p_digest)=32
        ON CONFLICT (token_digest) DO NOTHING
        RETURNING id
    )
    SELECT EXISTS(SELECT 1 FROM issued)
$$;
CREATE FUNCTION browseai.resolve_session(p_digest bytea)
RETURNS TABLE(user_id uuid,tenant_id uuid,role text)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
    SELECT s.user_id,s.tenant_id,m.role
    FROM browseai.auth_sessions s
    JOIN browseai.memberships m ON m.user_id=s.user_id AND m.tenant_id=s.tenant_id
    JOIN browseai.users u ON u.id=s.user_id AND u.enabled
    JOIN browseai.tenants t ON t.id=s.tenant_id AND t.enabled
    WHERE s.token_digest=p_digest AND s.revoked_at IS NULL
      AND s.expires_at>now()
$$;
CREATE FUNCTION browseai.revoke_session(p_digest bytea)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
    WITH revoked AS (
        UPDATE browseai.auth_sessions SET revoked_at=now()
        WHERE token_digest=p_digest AND revoked_at IS NULL
        RETURNING id
    )
    SELECT EXISTS(SELECT 1 FROM revoked)
$$;
REVOKE ALL ON FUNCTION browseai.issue_session(uuid,uuid,bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION browseai.resolve_session(bytea) FROM PUBLIC;
REVOKE ALL ON FUNCTION browseai.revoke_session(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.issue_session(uuid,uuid,bytea) TO browseai_auth;
GRANT EXECUTE ON FUNCTION browseai.resolve_session(bytea) TO browseai_auth;
GRANT EXECUTE ON FUNCTION browseai.revoke_session(bytea) TO browseai_auth;
COMMIT;
