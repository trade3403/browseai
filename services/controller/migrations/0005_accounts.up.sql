BEGIN;
GRANT USAGE ON SCHEMA browseai TO browseai_admin;

CREATE TABLE browseai.account_audit (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    operator_role text NOT NULL,
    user_id uuid NOT NULL REFERENCES browseai.users(id),
    action text NOT NULL CHECK (action IN ('provision','disable','enable','reset_password')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
REVOKE ALL ON browseai.account_audit FROM PUBLIC;

-- Revoke permanently on credential changes or either enabled-state change.
-- The issue functions lock the same identity rows before inserting a session.
CREATE FUNCTION browseai.revoke_changed_identity() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp
AS $$
BEGIN
    IF TG_TABLE_NAME='users' THEN
        UPDATE browseai.auth_sessions SET revoked_at=clock_timestamp()
        WHERE user_id=NEW.id AND revoked_at IS NULL;
    ELSE
        UPDATE browseai.auth_sessions SET revoked_at=clock_timestamp()
        WHERE tenant_id=NEW.id AND revoked_at IS NULL;
    END IF;
    RETURN NEW;
END
$$;
REVOKE ALL ON FUNCTION browseai.revoke_changed_identity() FROM PUBLIC;
CREATE TRIGGER revoke_user_sessions AFTER UPDATE OF password_hash,enabled ON browseai.users
FOR EACH ROW WHEN (OLD.password_hash IS DISTINCT FROM NEW.password_hash OR OLD.enabled IS DISTINCT FROM NEW.enabled)
EXECUTE FUNCTION browseai.revoke_changed_identity();
CREATE TRIGGER revoke_tenant_sessions AFTER UPDATE OF enabled ON browseai.tenants
FOR EACH ROW WHEN (OLD.enabled IS DISTINCT FROM NEW.enabled)
EXECUTE FUNCTION browseai.revoke_changed_identity();

CREATE OR REPLACE FUNCTION browseai.issue_session(p_user uuid,p_tenant uuid,p_digest bytea)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path=pg_catalog,pg_temp
AS $$
BEGIN
    IF p_digest IS NULL OR octet_length(p_digest)<>32 THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.users WHERE id=p_user AND enabled FOR SHARE;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.tenants WHERE id=p_tenant AND enabled FOR SHARE;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.memberships WHERE user_id=p_user AND tenant_id=p_tenant FOR SHARE;
    IF NOT FOUND THEN RETURN false; END IF;
    INSERT INTO browseai.auth_sessions(id,tenant_id,user_id,token_digest,expires_at)
    VALUES(gen_random_uuid(),p_tenant,p_user,p_digest,clock_timestamp()+interval '8 hours')
    ON CONFLICT(token_digest) DO NOTHING;
    RETURN FOUND;
END
$$;

-- Reject a password proof invalidated while Argon2id was running.
CREATE FUNCTION browseai.issue_verified_session(p_user uuid,p_tenant uuid,p_digest bytea,p_hash text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path=pg_catalog,pg_temp
AS $$
BEGIN
    PERFORM 1 FROM browseai.users WHERE id=p_user AND enabled AND password_hash=p_hash FOR SHARE;
    IF NOT FOUND THEN RETURN false; END IF;
    RETURN browseai.issue_session(p_user,p_tenant,p_digest);
END
$$;
REVOKE ALL ON FUNCTION browseai.issue_verified_session(uuid,uuid,bytea,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.issue_verified_session(uuid,uuid,bytea,text) TO browseai_auth;

CREATE FUNCTION browseai.valid_password_hash(p_hash text) RETURNS boolean
LANGUAGE sql IMMUTABLE SECURITY INVOKER SET search_path=pg_catalog
AS $$ SELECT coalesce(p_hash ~ '^\$argon2id\$v=19\$m=65536,t=3,p=1\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$',false) $$;
REVOKE ALL ON FUNCTION browseai.valid_password_hash(text) FROM PUBLIC;

-- Trusted offline operator only; one new account owns one new workspace.
CREATE FUNCTION browseai.provision_account(p_login text,p_name text,p_hash text)
RETURNS uuid LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path=pg_catalog,pg_temp
AS $$
DECLARE uid uuid := gen_random_uuid();
        tid uuid := gen_random_uuid();
BEGIN
    IF p_login IS NULL OR p_login !~ '^[A-Za-z0-9][A-Za-z0-9_.@+-]{0,253}$'
       OR p_name IS NULL OR length(p_name) NOT BETWEEN 1 AND 128
       OR NOT browseai.valid_password_hash(p_hash) THEN
        RAISE EXCEPTION 'invalid account input' USING ERRCODE='22023';
    END IF;
    INSERT INTO browseai.tenants(id,name) VALUES(tid,p_name);
    INSERT INTO browseai.users(id,login,password_hash) VALUES(uid,p_login,p_hash);
    INSERT INTO browseai.memberships(tenant_id,user_id,role) VALUES(tid,uid,'user');
    INSERT INTO browseai.account_audit(operator_role,user_id,action) VALUES(session_user,uid,'provision');
    RETURN uid;
END
$$;
CREATE FUNCTION browseai.set_account_enabled(p_login text,p_enabled boolean)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path=pg_catalog,pg_temp
AS $$
DECLARE uid uuid;
BEGIN
    UPDATE browseai.users SET enabled=p_enabled
    WHERE login=p_login AND enabled IS DISTINCT FROM p_enabled
    RETURNING id INTO uid;
    IF NOT FOUND THEN RETURN false; END IF;
    INSERT INTO browseai.account_audit(operator_role,user_id,action)
    VALUES(session_user,uid,CASE WHEN p_enabled THEN 'enable' ELSE 'disable' END);
    RETURN true;
END
$$;
CREATE FUNCTION browseai.reset_account_password(p_login text,p_hash text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER
SET search_path=pg_catalog,pg_temp
AS $$
DECLARE uid uuid;
BEGIN
    IF NOT browseai.valid_password_hash(p_hash) THEN
        RAISE EXCEPTION 'invalid password hash' USING ERRCODE='22023';
    END IF;
    UPDATE browseai.users SET password_hash=p_hash WHERE login=p_login RETURNING id INTO uid;
    IF NOT FOUND THEN RETURN false; END IF;
    INSERT INTO browseai.account_audit(operator_role,user_id,action) VALUES(session_user,uid,'reset_password');
    RETURN true;
END
$$;
REVOKE ALL ON FUNCTION browseai.provision_account(text,text,text) FROM PUBLIC;
REVOKE ALL ON FUNCTION browseai.set_account_enabled(text,boolean) FROM PUBLIC;
REVOKE ALL ON FUNCTION browseai.reset_account_password(text,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.provision_account(text,text,text) TO browseai_admin;
GRANT EXECUTE ON FUNCTION browseai.set_account_enabled(text,boolean) TO browseai_admin;
GRANT EXECUTE ON FUNCTION browseai.reset_account_password(text,text) TO browseai_admin;
COMMIT;
