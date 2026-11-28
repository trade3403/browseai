BEGIN;
SELECT browseai.provision_github_binding('00000000-0000-4000-8000-000000000001',99402,99401,'owner/verifier',12,34);
DO $$
DECLARE
    t uuid:='00000000-0000-4000-8000-000000000001';
    other_t uuid:='00000000-0000-4000-8000-000000000002';
    v uuid:=gen_random_uuid(); k uuid:=gen_random_uuid(); k2 uuid:=gen_random_uuid(); k3 uuid:=gen_random_uuid();
    a uuid:=gen_random_uuid(); n timestamptz:=clock_timestamp();
    pub bytea:=decode(repeat('ab',32),'hex'); pub2 bytea:=decode(repeat('cd',32),'hex');
BEGIN
    SET LOCAL ROLE browseai_app;
    BEGIN
        PERFORM browseai.register_github_verifier(t,99402,99401,v,k,pub,n-interval '1 minute',n+interval '1 day',a);
        RAISE EXCEPTION 'app registration allowed';
    EXCEPTION WHEN insufficient_privilege THEN NULL; END;
    BEGIN
        PERFORM browseai.resolve_github_verifier(t,99402,99401,v,k,n);
        RAISE EXCEPTION 'app lookup allowed';
    EXCEPTION WHEN insufficient_privilege THEN NULL; END;
    RESET ROLE;
    SET LOCAL ROLE browseai_verifier_operator;
    IF NOT browseai.register_github_verifier(t,99402,99401,v,k,pub,n-interval '1 minute',n+interval '1 day',a) THEN RAISE EXCEPTION 'register failed'; END IF;
    BEGIN
        PERFORM browseai.register_github_verifier(t,99402,99401,v,k2,pub2,n,n+interval '1 day',a);
        RAISE EXCEPTION 'approval reused';
    EXCEPTION WHEN unique_violation THEN NULL; END;
    BEGIN
        PERFORM browseai.register_github_verifier(other_t,99402,99401,v,k2,pub2,n,n+interval '1 day',gen_random_uuid());
        RAISE EXCEPTION 'cross tenant registration allowed';
    EXCEPTION WHEN raise_exception THEN
        IF SQLERRM<>'verifier binding unavailable' THEN RAISE; END IF;
    END;
    BEGIN
        PERFORM browseai.register_github_verifier(t,99402,99401,v,k2,pub2,n,n+interval '100 days',gen_random_uuid());
        RAISE EXCEPTION 'overlong lifetime allowed';
    EXCEPTION WHEN invalid_parameter_value THEN NULL; END;
    IF NOT browseai.register_github_verifier(t,99402,99401,v,k2,pub2,n,n+interval '1 day',gen_random_uuid()) THEN RAISE EXCEPTION 'rotation failed'; END IF;
    IF NOT browseai.register_github_verifier(t,99402,99401,v,k3,decode(repeat('ef',32),'hex'),n+interval '1 day',n+interval '2 days',gen_random_uuid()) THEN RAISE EXCEPTION 'future registration failed'; END IF;
    BEGIN
        UPDATE browseai.github_verifier_keys SET revoked_at=NULL;
        RAISE EXCEPTION 'operator table mutation allowed';
    EXCEPTION WHEN insufficient_privilege THEN NULL; END;
    RESET ROLE;
    SET LOCAL ROLE browseai_verifier_reader;
    IF browseai.resolve_github_verifier(t,99402,99401,v,k,n) IS DISTINCT FROM pub THEN RAISE EXCEPTION 'lookup failed'; END IF;
    IF browseai.resolve_github_verifier(t,99402,99401,v,k2,n) IS DISTINCT FROM pub2 THEN RAISE EXCEPTION 'rotation lookup failed'; END IF;
    IF browseai.resolve_github_verifier(other_t,99402,99401,v,k,n) IS NOT NULL
       OR browseai.resolve_github_verifier(t,99403,99401,v,k,n) IS NOT NULL
       OR browseai.resolve_github_verifier(t,99402,99402,v,k,n) IS NOT NULL
       OR browseai.resolve_github_verifier(t,99402,99401,gen_random_uuid(),k,n) IS NOT NULL
       OR browseai.resolve_github_verifier(t,99402,99401,v,k,n-interval '2 minutes') IS NOT NULL
       OR browseai.resolve_github_verifier(t,99402,99401,v,k,n+interval '1 hour') IS NOT NULL
       OR browseai.resolve_github_verifier(t,99402,99401,v,k3,n) IS NOT NULL THEN RAISE EXCEPTION 'invalid key accepted'; END IF;
    BEGIN
        PERFORM browseai.revoke_github_verifier(t,99402,99401,k,gen_random_uuid());
        RAISE EXCEPTION 'reader revocation allowed';
    EXCEPTION WHEN insufficient_privilege THEN NULL; END;
    BEGIN
        PERFORM 1 FROM browseai.github_verifier_keys;
        RAISE EXCEPTION 'reader raw table access allowed';
    EXCEPTION WHEN insufficient_privilege THEN NULL; END;
    RESET ROLE;
    SET LOCAL ROLE browseai_verifier_operator;
    IF NOT browseai.revoke_github_verifier(t,99402,99401,k,gen_random_uuid()) THEN RAISE EXCEPTION 'revoke failed'; END IF;
    IF browseai.revoke_github_verifier(t,99402,99401,k,gen_random_uuid()) THEN RAISE EXCEPTION 'repeat revoke allowed'; END IF;
    RESET ROLE;
    SET LOCAL ROLE browseai_verifier_reader;
    IF browseai.resolve_github_verifier(t,99402,99401,v,k,n) IS NOT NULL THEN RAISE EXCEPTION 'revoked key accepted'; END IF;
    IF browseai.resolve_github_verifier(t,99402,99401,v,k2,n) IS DISTINCT FROM pub2 THEN RAISE EXCEPTION 'rotation lost'; END IF;
    RESET ROLE;
    UPDATE browseai.github_repositories SET enabled=false WHERE tenant_id=t AND repository_id=99401;
    SET LOCAL ROLE browseai_verifier_reader;
    IF browseai.resolve_github_verifier(t,99402,99401,v,k2,n) IS NOT NULL THEN RAISE EXCEPTION 'disabled binding accepted'; END IF;
    RESET ROLE;
    IF (SELECT count(*) FROM browseai.github_verifier_audit WHERE key_id IN(k,k2,k3))<>4 THEN RAISE EXCEPTION 'audit count incorrect'; END IF;
END $$;
ROLLBACK;
