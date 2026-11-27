-- All identities and digests below are synthetic CI fixtures.
INSERT INTO browseai.users(id,login,password_hash) VALUES
('00000000-0000-4000-8000-000000000031','mock-a','not-a-real-password-hash'),
('00000000-0000-4000-8000-000000000032','mock-b','not-a-real-password-hash');
INSERT INTO browseai.memberships(tenant_id,user_id,role) VALUES
('00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000031','user'),
('00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000032','user');
BEGIN;
SET LOCAL ROLE browseai_auth;
DO $$
DECLARE a bytea := decode(repeat('ab',32),'hex');
b bytea := decode(repeat('cd',32),'hex');
BEGIN
    IF NOT browseai.issue_session('00000000-0000-4000-8000-000000000031','00000000-0000-4000-8000-000000000001',a) THEN
        RAISE EXCEPTION 'issue A failed';
    END IF;
    IF browseai.issue_session('00000000-0000-4000-8000-000000000031','00000000-0000-4000-8000-000000000002',b) THEN
        RAISE EXCEPTION 'cross membership issue';
    END IF;
    IF NOT browseai.issue_session('00000000-0000-4000-8000-000000000032','00000000-0000-4000-8000-000000000002',b) THEN
        RAISE EXCEPTION 'issue B failed';
    END IF;
    IF browseai.issue_session('00000000-0000-4000-8000-000000000031','00000000-0000-4000-8000-000000000001',a) THEN
        RAISE EXCEPTION 'duplicate digest accepted';
    END IF;
    IF (SELECT count(*) FROM browseai.resolve_session(a)) <> 1 OR
       NOT EXISTS (SELECT 1 FROM browseai.resolve_session(a) WHERE tenant_id='00000000-0000-4000-8000-000000000001' AND role='user') OR
       NOT EXISTS (SELECT 1 FROM browseai.resolve_session(b) WHERE tenant_id='00000000-0000-4000-8000-000000000002') THEN
        RAISE EXCEPTION 'session principal mismatch';
    END IF;
    IF NOT browseai.revoke_session(a) THEN
        RAISE EXCEPTION 'session revoke failed';
    END IF;
    IF EXISTS (SELECT 1 FROM browseai.resolve_session(a)) OR browseai.revoke_session(a) THEN
        RAISE EXCEPTION 'revoked session still active';
    END IF;
    IF EXISTS (SELECT 1 FROM browseai.resolve_session(decode(repeat('ff',32),'hex'))) THEN
        RAISE EXCEPTION 'unknown digest accepted';
    END IF;
    BEGIN
        PERFORM password_hash FROM browseai.users;
        RAISE EXCEPTION 'auth role can read credentials directly';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
    BEGIN
        PERFORM id FROM browseai.projects;
        RAISE EXCEPTION 'auth role can read project data';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
END $$;
COMMIT;
UPDATE browseai.users SET enabled=false WHERE login='mock-b';
BEGIN;
SET LOCAL ROLE browseai_auth;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM browseai.resolve_session(decode(repeat('cd',32),'hex'))) THEN
        RAISE EXCEPTION 'disabled user session accepted';
    END IF;
END $$;
COMMIT;
UPDATE browseai.users SET enabled=true WHERE login='mock-b';
UPDATE browseai.tenants SET enabled=false WHERE name='Tenant B';
BEGIN;
SET LOCAL ROLE browseai_auth;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM browseai.resolve_session(decode(repeat('cd',32),'hex'))) THEN
        RAISE EXCEPTION 'disabled tenant session accepted';
    END IF;
END $$;
COMMIT;
UPDATE browseai.tenants SET enabled=true WHERE name='Tenant B';
UPDATE browseai.auth_sessions SET created_at=now()-interval '2 hours',expires_at=now()-interval '1 hour'
WHERE token_digest=decode(repeat('cd',32),'hex');
BEGIN;
SET LOCAL ROLE browseai_auth;
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM browseai.resolve_session(decode(repeat('cd',32),'hex'))) THEN
        RAISE EXCEPTION 'expired session accepted';
    END IF;
END $$;
COMMIT;
BEGIN;
SET LOCAL ROLE browseai_app;
DO $$
BEGIN
    BEGIN
        PERFORM browseai.resolve_session(decode(repeat('cd',32),'hex'));
        RAISE EXCEPTION 'task role can resolve arbitrary sessions';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
END $$;
COMMIT;
