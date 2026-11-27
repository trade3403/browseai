BEGIN;
DROP FUNCTION browseai.reset_account_password(text,text);
DROP FUNCTION browseai.set_account_enabled(text,boolean);
DROP FUNCTION browseai.provision_account(text,text,text);
DROP FUNCTION browseai.valid_password_hash(text);
DROP FUNCTION browseai.issue_verified_session(uuid,uuid,bytea,text);
DROP TRIGGER revoke_tenant_sessions ON browseai.tenants;
DROP TRIGGER revoke_user_sessions ON browseai.users;
DROP FUNCTION browseai.revoke_changed_identity();
CREATE OR REPLACE FUNCTION browseai.issue_session(p_user uuid, p_tenant uuid, p_digest bytea)
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
DROP TABLE browseai.account_audit;
REVOKE USAGE ON SCHEMA browseai FROM browseai_admin;
COMMIT;
