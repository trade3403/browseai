BEGIN;
GRANT USAGE ON SCHEMA browseai TO browseai_ci_acceptor,browseai_ci_approval_operator;
CREATE TABLE browseai.github_ci_acceptance_grants (
    id uuid PRIMARY KEY,
    tenant_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_revision bigint NOT NULL CHECK(task_revision>=0),
    actor_id uuid NOT NULL REFERENCES browseai.users(id),
    push_id uuid NOT NULL REFERENCES browseai.github_ci_evidence(id),
    pr_id uuid NOT NULL REFERENCES browseai.github_ci_evidence(id),
    push_digest bytea NOT NULL CHECK(octet_length(push_digest)=32),
    pr_digest bytea NOT NULL CHECK(octet_length(pr_digest)=32),
    workflow_id bigint NOT NULL CHECK(workflow_id>0),
    source_digest text NOT NULL CHECK(source_digest ~ '^[0-9a-f]{64}$'),
    approved_by text NOT NULL DEFAULT session_user,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    revoked_by text,
    acceptance_id uuid UNIQUE REFERENCES browseai.github_ci_acceptance(approval_id),
    FOREIGN KEY(tenant_id,task_id) REFERENCES browseai.tasks(tenant_id,id),
    UNIQUE(tenant_id,task_id,task_revision),
    CHECK(push_id<>pr_id AND push_digest<>pr_digest),
    CHECK(isfinite(created_at) AND isfinite(expires_at) AND expires_at>created_at AND expires_at<=created_at+interval '30 seconds'),
    CHECK((revoked_at IS NULL)=(revoked_by IS NULL))
);
REVOKE ALL ON browseai.github_ci_acceptance_grants FROM PUBLIC;
ALTER TABLE browseai.github_ci_acceptance_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.github_ci_acceptance_grants FORCE ROW LEVEL SECURITY;

-- The operator attests the named administrator's review of exact evidence.
-- This is not an authenticated web session or a GitHub signature verifier.
CREATE FUNCTION browseai.issue_github_ci_acceptance_grant(p_id uuid,p_tenant uuid,p_task uuid,p_revision bigint,p_actor uuid,
    p_push uuid,p_push_digest bytea,p_pr uuid,p_pr_digest bytea,p_workflow bigint,p_source text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE x browseai.github_ci_evidence%ROWTYPE; y browseai.github_ci_evidence%ROWTYPE; n timestamptz; expiry timestamptz;
BEGIN
    IF session_user<>'browseai_ci_approval_operator' OR p_id IS NULL OR p_actor IS NULL OR p_revision IS NULL
      OR p_revision<0 OR p_revision=9223372036854775807 OR p_push IS NULL OR p_pr IS NULL OR p_push=p_pr
      OR p_push_digest IS NULL OR octet_length(p_push_digest)<>32 OR p_pr_digest IS NULL OR octet_length(p_pr_digest)<>32
      OR p_push_digest=p_pr_digest OR p_workflow IS NULL OR p_workflow<=0 OR p_source IS NULL OR p_source !~ '^[0-9a-f]{64}$' THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.tasks WHERE tenant_id=p_tenant AND id=p_task AND revision=p_revision AND state='WAIT_CI' FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.memberships m JOIN browseai.users u ON u.id=m.user_id JOIN browseai.tenants w ON w.id=m.tenant_id
      WHERE m.tenant_id=p_tenant AND m.user_id=p_actor AND m.role='platform_admin' AND u.enabled AND w.enabled FOR SHARE OF m,u,w;
    IF NOT FOUND THEN RETURN false; END IF;
    SELECT * INTO x FROM browseai.github_ci_evidence WHERE id=p_push AND tenant_id=p_tenant AND task_id=p_task
      AND task_revision=p_revision AND event_type='push' AND workflow_id=p_workflow AND source_digest=p_source
      AND evidence_digest=p_push_digest AND consumed_at IS NULL FOR SHARE;
    IF NOT FOUND THEN RETURN false; END IF;
    SELECT * INTO y FROM browseai.github_ci_evidence WHERE id=p_pr AND tenant_id=p_tenant AND task_id=p_task
      AND task_revision=p_revision AND event_type='pull_request' AND workflow_id=p_workflow AND source_digest=p_source
      AND evidence_digest=p_pr_digest AND consumed_at IS NULL FOR SHARE;
    IF NOT FOUND OR x.head_sha<>y.head_sha OR x.project_id<>y.project_id OR x.repository_id<>y.repository_id
      OR x.installation_id<>y.installation_id OR x.code_branch<>y.code_branch OR x.workflow_name<>y.workflow_name
      OR x.workflow_path<>y.workflow_path OR x.required_jobs<>y.required_jobs THEN RETURN false; END IF;
    n:=clock_timestamp(); expiry:=least(x.expires_at,y.expires_at,n+interval '30 seconds');
    IF expiry<=n OR n<x.observed_at OR n<y.observed_at THEN RETURN false; END IF;
    INSERT INTO browseai.github_ci_acceptance_grants(id,tenant_id,task_id,task_revision,actor_id,push_id,pr_id,push_digest,pr_digest,workflow_id,source_digest,created_at,expires_at)
    VALUES(p_id,p_tenant,p_task,p_revision,p_actor,p_push,p_pr,p_push_digest,p_pr_digest,p_workflow,p_source,n,expiry)
    ON CONFLICT DO NOTHING;
    RETURN FOUND;
END $$;

CREATE FUNCTION browseai.revoke_github_ci_acceptance_grant(p_id uuid,p_tenant uuid)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF session_user<>'browseai_ci_approval_operator' THEN RETURN false; END IF;
    UPDATE browseai.github_ci_acceptance_grants SET revoked_at=clock_timestamp(),revoked_by=session_user
      WHERE id=p_id AND tenant_id=p_tenant AND revoked_at IS NULL AND acceptance_id IS NULL;
    RETURN FOUND;
END $$;

CREATE FUNCTION browseai.accept_authorized_github_ci(p_id uuid,p_tenant uuid,p_task uuid,p_revision bigint,p_actor uuid,
    p_push uuid,p_push_digest bytea,p_pr uuid,p_pr_digest bytea,p_workflow bigint,p_source text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE g browseai.github_ci_acceptance_grants%ROWTYPE;
BEGIN
    IF session_user<>'browseai_ci_acceptor' OR p_id IS NULL THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.tasks WHERE tenant_id=p_tenant AND id=p_task AND revision=p_revision AND state='WAIT_CI' FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    SELECT * INTO g FROM browseai.github_ci_acceptance_grants WHERE id=p_id AND tenant_id=p_tenant AND task_id=p_task
      AND task_revision=p_revision AND actor_id=p_actor AND push_id=p_push AND pr_id=p_pr
      AND push_digest=p_push_digest AND pr_digest=p_pr_digest AND workflow_id=p_workflow AND source_digest=p_source
      AND revoked_at IS NULL AND acceptance_id IS NULL AND created_at<=clock_timestamp() AND expires_at>clock_timestamp() FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    BEGIN
        IF NOT browseai.accept_github_ci(p_tenant,p_task,p_revision,p_actor,p_id,p_push,p_push_digest,p_pr,p_pr_digest,p_workflow,p_source)
          OR clock_timestamp()>=g.expires_at THEN RAISE EXCEPTION 'acceptance grant unavailable' USING ERRCODE='PBA02'; END IF;
        UPDATE browseai.github_ci_acceptance_grants SET acceptance_id=p_id WHERE id=p_id AND acceptance_id IS NULL;
        IF NOT FOUND THEN RAISE EXCEPTION 'acceptance grant unavailable' USING ERRCODE='PBA02'; END IF;
        RETURN true;
    EXCEPTION WHEN SQLSTATE 'PBA02' OR unique_violation THEN RETURN false;
    END;
END $$;
REVOKE ALL ON FUNCTION browseai.issue_github_ci_acceptance_grant(uuid,uuid,uuid,bigint,uuid,uuid,bytea,uuid,bytea,bigint,text),
    browseai.revoke_github_ci_acceptance_grant(uuid,uuid),browseai.accept_authorized_github_ci(uuid,uuid,uuid,bigint,uuid,uuid,bytea,uuid,bytea,bigint,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.issue_github_ci_acceptance_grant(uuid,uuid,uuid,bigint,uuid,uuid,bytea,uuid,bytea,bigint,text),
    browseai.revoke_github_ci_acceptance_grant(uuid,uuid) TO browseai_ci_approval_operator;
GRANT EXECUTE ON FUNCTION browseai.accept_authorized_github_ci(uuid,uuid,uuid,bigint,uuid,uuid,bytea,uuid,bytea,bigint,text) TO browseai_ci_acceptor;
COMMIT;
