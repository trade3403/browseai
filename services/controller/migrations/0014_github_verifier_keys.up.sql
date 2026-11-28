BEGIN;
-- Dedicated operator and internal evidence reader. Neither receives table access.
GRANT USAGE ON SCHEMA browseai TO browseai_verifier_operator,browseai_verifier_reader;
CREATE TABLE browseai.github_verifier_keys (
    key_id uuid PRIMARY KEY,
    verifier_id uuid NOT NULL,
    tenant_id uuid NOT NULL,
    installation_id bigint NOT NULL,
    repository_id bigint NOT NULL,
    public_key bytea NOT NULL UNIQUE CHECK (octet_length(public_key)=32 AND public_key<>decode(repeat('00',32),'hex') AND public_key<>decode('01'||repeat('00',31),'hex')),
    valid_from timestamptz NOT NULL,
    valid_until timestamptz NOT NULL,
    revoked_at timestamptz,
    FOREIGN KEY (tenant_id,repository_id) REFERENCES browseai.github_repositories(tenant_id,repository_id),
    FOREIGN KEY (tenant_id,installation_id) REFERENCES browseai.github_installations(tenant_id,installation_id),
    CHECK (isfinite(valid_from) AND isfinite(valid_until) AND valid_until>valid_from AND valid_until<=valid_from+interval '90 days')
);
CREATE TABLE browseai.github_verifier_audit (
    approval_id uuid PRIMARY KEY,
    key_id uuid NOT NULL REFERENCES browseai.github_verifier_keys(key_id),
    operation text NOT NULL CHECK (operation IN ('register','revoke')),
    operator_role text NOT NULL DEFAULT session_user,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
REVOKE ALL ON browseai.github_verifier_keys,browseai.github_verifier_audit FROM PUBLIC;

-- Approval ID is an externally reviewed, one-use operator reference, not a
-- public API credential. The operator must independently verify key ownership.
CREATE FUNCTION browseai.register_github_verifier(p_tenant uuid,p_installation bigint,p_repository bigint,p_verifier uuid,p_key uuid,p_public bytea,p_from timestamptz,p_until timestamptz,p_approval uuid)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE n timestamptz:=clock_timestamp();
BEGIN
    IF p_verifier IS NULL OR p_key IS NULL OR p_approval IS NULL OR p_public IS NULL
       OR octet_length(p_public)<>32 OR p_from IS NULL OR p_until IS NULL
       OR NOT isfinite(p_from) OR NOT isfinite(p_until) OR p_from<n-interval '5 minutes'
       OR p_from>n+interval '30 days' OR p_until<=n OR p_until<=p_from OR p_until>p_from+interval '90 days' THEN
        RAISE EXCEPTION 'invalid verifier registration' USING ERRCODE='22023';
    END IF;
    PERFORM 1 FROM browseai.tenants t
    JOIN browseai.github_installations i ON i.tenant_id=t.id
    JOIN browseai.github_repositories r ON r.tenant_id=t.id AND r.installation_id=i.installation_id
    WHERE t.id=p_tenant AND i.installation_id=p_installation AND r.repository_id=p_repository
      AND t.enabled AND i.enabled AND r.enabled FOR SHARE OF t,i,r;
    IF NOT FOUND THEN RAISE EXCEPTION 'verifier binding unavailable'; END IF;
    INSERT INTO browseai.github_verifier_keys(key_id,verifier_id,tenant_id,installation_id,repository_id,public_key,valid_from,valid_until)
    VALUES(p_key,p_verifier,p_tenant,p_installation,p_repository,p_public,p_from,p_until);
    INSERT INTO browseai.github_verifier_audit(approval_id,key_id,operation) VALUES(p_approval,p_key,'register');
    RETURN true;
END $$;

-- Revocation is irreversible, even if the repository binding is disabled.
CREATE FUNCTION browseai.revoke_github_verifier(p_tenant uuid,p_installation bigint,p_repository bigint,p_key uuid,p_approval uuid)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF p_approval IS NULL THEN RAISE EXCEPTION 'approval required' USING ERRCODE='22023'; END IF;
    UPDATE browseai.github_verifier_keys SET revoked_at=clock_timestamp()
    WHERE tenant_id=p_tenant AND installation_id=p_installation AND repository_id=p_repository
      AND key_id=p_key AND revoked_at IS NULL;
    IF NOT FOUND THEN RETURN false; END IF;
    INSERT INTO browseai.github_verifier_audit(approval_id,key_id,operation) VALUES(p_approval,p_key,'revoke');
    RETURN true;
END $$;

-- Exact scope, live authorization and validity at both issuance and lookup.
-- Consumers must recheck in the eventual atomic evidence-consumption transaction.
CREATE FUNCTION browseai.resolve_github_verifier(p_tenant uuid,p_installation bigint,p_repository bigint,p_verifier uuid,p_key uuid,p_issued timestamptz)
RETURNS bytea LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE n timestamptz:=clock_timestamp(); k bytea;
BEGIN
    IF p_issued IS NULL OR NOT isfinite(p_issued) OR p_issued>n OR p_issued<=n-interval '24 hours' THEN RETURN NULL; END IF;
    SELECT v.public_key INTO k FROM browseai.github_verifier_keys v
    JOIN browseai.tenants t ON t.id=v.tenant_id
    JOIN browseai.github_installations i ON i.tenant_id=v.tenant_id AND i.installation_id=v.installation_id
    JOIN browseai.github_repositories r ON r.tenant_id=v.tenant_id AND r.installation_id=v.installation_id AND r.repository_id=v.repository_id
    WHERE v.key_id=p_key AND v.verifier_id=p_verifier AND v.tenant_id=p_tenant
      AND v.installation_id=p_installation AND v.repository_id=p_repository AND v.revoked_at IS NULL
      AND t.enabled AND i.enabled AND r.enabled
      AND v.valid_from<=p_issued AND v.valid_until>p_issued AND v.valid_until>n;
    RETURN k;
END $$;
REVOKE ALL ON FUNCTION browseai.register_github_verifier(uuid,bigint,bigint,uuid,uuid,bytea,timestamptz,timestamptz,uuid),browseai.revoke_github_verifier(uuid,bigint,bigint,uuid,uuid),browseai.resolve_github_verifier(uuid,bigint,bigint,uuid,uuid,timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.register_github_verifier(uuid,bigint,bigint,uuid,uuid,bytea,timestamptz,timestamptz,uuid),browseai.revoke_github_verifier(uuid,bigint,bigint,uuid,uuid) TO browseai_verifier_operator;
GRANT EXECUTE ON FUNCTION browseai.resolve_github_verifier(uuid,bigint,bigint,uuid,uuid,timestamptz) TO browseai_verifier_reader;
COMMIT;
