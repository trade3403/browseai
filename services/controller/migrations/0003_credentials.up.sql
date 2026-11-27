BEGIN;
-- Only the trusted password verifier may look up a single enabled membership.
-- Multiple memberships fail closed until explicit workspace selection exists.
CREATE FUNCTION browseai.login_candidate(p_login text)
RETURNS TABLE(user_id uuid,tenant_id uuid,role text,password_hash text)
LANGUAGE sql STABLE SECURITY DEFINER
SET search_path = pg_catalog, pg_temp
AS $$
    WITH candidates AS (
        SELECT u.id AS user_id,m.tenant_id,m.role,u.password_hash
        FROM browseai.users u
        JOIN browseai.memberships m ON m.user_id=u.id
        JOIN browseai.tenants t ON t.id=m.tenant_id AND t.enabled
        WHERE u.login=p_login AND u.enabled
    )
    SELECT c.user_id,c.tenant_id,c.role,c.password_hash
    FROM candidates c
    WHERE (SELECT count(*) FROM candidates)=1
$$;
REVOKE ALL ON FUNCTION browseai.login_candidate(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.login_candidate(text) TO browseai_auth;
COMMIT;
