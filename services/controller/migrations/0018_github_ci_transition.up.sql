BEGIN;
-- Sealed transaction foundation. No runtime caller can invoke this until
-- independent policy, live observation and human acceptance are wired.
CREATE TABLE browseai.github_ci_acceptance (
    approval_id uuid PRIMARY KEY REFERENCES browseai.audit_logs(id),
    tenant_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_revision bigint NOT NULL CHECK (task_revision>=0),
    push_id uuid NOT NULL UNIQUE REFERENCES browseai.github_ci_evidence(id),
    pr_id uuid NOT NULL UNIQUE REFERENCES browseai.github_ci_evidence(id),
    CHECK (push_id<>pr_id),
    UNIQUE(tenant_id,task_id,task_revision)
);
REVOKE ALL ON browseai.github_ci_acceptance FROM PUBLIC;
ALTER TABLE browseai.github_ci_acceptance ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.github_ci_acceptance FORCE ROW LEVEL SECURITY;

CREATE FUNCTION browseai.accept_github_ci(p_tenant uuid,p_task uuid,p_revision bigint,p_actor uuid,p_approval uuid,
    p_push uuid,p_push_digest bytea,p_pr uuid,p_pr_digest bytea,p_workflow bigint,p_source text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE push_row browseai.github_ci_evidence%ROWTYPE; pr_row browseai.github_ci_evidence%ROWTYPE; n timestamptz;
BEGIN
    IF p_tenant IS NULL OR p_task IS NULL OR p_revision IS NULL OR p_revision<0 OR p_revision=9223372036854775807
       OR p_actor IS NULL OR p_approval IS NULL OR p_push IS NULL OR p_pr IS NULL OR p_push=p_pr
       OR p_push_digest IS NULL OR octet_length(p_push_digest)<>32 OR p_pr_digest IS NULL OR octet_length(p_pr_digest)<>32
       OR p_workflow IS NULL OR p_workflow<=0 OR p_source IS NULL OR p_source !~ '^[0-9a-f]{64}$' THEN RETURN false; END IF;
    -- Lock task first, matching the single-row consumption lock order.
    PERFORM 1 FROM browseai.tasks WHERE tenant_id=p_tenant AND id=p_task
      AND revision=p_revision AND state='WAIT_CI' FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.memberships m JOIN browseai.users u ON u.id=m.user_id
    JOIN browseai.tenants t ON t.id=m.tenant_id
    WHERE m.tenant_id=p_tenant AND m.user_id=p_actor AND m.role='platform_admin'
      AND u.enabled AND t.enabled FOR SHARE OF m,u,t;
    IF NOT FOUND THEN RETURN false; END IF;
    SELECT * INTO push_row FROM browseai.github_ci_evidence WHERE id=p_push AND tenant_id=p_tenant
      AND task_id=p_task AND task_revision=p_revision AND event_type='push'
      AND workflow_id=p_workflow AND source_digest=p_source AND evidence_digest=p_push_digest
      AND consumed_at IS NULL FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    SELECT * INTO pr_row FROM browseai.github_ci_evidence WHERE id=p_pr AND tenant_id=p_tenant
      AND task_id=p_task AND task_revision=p_revision AND event_type='pull_request'
      AND workflow_id=p_workflow AND source_digest=p_source AND evidence_digest=p_pr_digest
      AND consumed_at IS NULL FOR UPDATE;
    IF NOT FOUND OR push_row.head_sha<>pr_row.head_sha OR push_row.project_id<>pr_row.project_id
       OR push_row.repository_id<>pr_row.repository_id OR push_row.installation_id<>pr_row.installation_id
       OR push_row.code_branch<>pr_row.code_branch OR push_row.workflow_name<>pr_row.workflow_name
       OR push_row.workflow_path<>pr_row.workflow_path OR push_row.required_jobs<>pr_row.required_jobs THEN RETURN false; END IF;
    -- The exception block is a subtransaction. Failure of the second consume,
    -- final freshness check, audit, or state update must undo the first.
    BEGIN
        IF NOT browseai.consume_github_ci_evidence(p_tenant,p_task,p_revision,p_push,p_push_digest)
           THEN RAISE EXCEPTION 'CI acceptance unavailable' USING ERRCODE='PBA01'; END IF;
        IF NOT browseai.consume_github_ci_evidence(p_tenant,p_task,p_revision,p_pr,p_pr_digest)
           THEN RAISE EXCEPTION 'CI acceptance unavailable' USING ERRCODE='PBA01'; END IF;
        n:=clock_timestamp();
        IF n>=least(push_row.expires_at,pr_row.expires_at)
           THEN RAISE EXCEPTION 'CI acceptance unavailable' USING ERRCODE='PBA01'; END IF;
        UPDATE browseai.tasks SET state='STAGE_PASSED',revision=revision+1,updated_at=n
        WHERE tenant_id=p_tenant AND id=p_task AND revision=p_revision AND state='WAIT_CI';
        IF NOT FOUND THEN RAISE EXCEPTION 'CI acceptance unavailable' USING ERRCODE='PBA01'; END IF;
        INSERT INTO browseai.audit_logs(id,tenant_id,actor_id,action,resource_id,revision)
        VALUES(p_approval,p_tenant,p_actor,'task.ci_pass',p_task,p_revision+1);
        INSERT INTO browseai.events(tenant_id,audit_id,kind,resource_id,revision)
        VALUES(p_tenant,p_approval,'task.ci_pass',p_task,p_revision+1);
        INSERT INTO browseai.github_ci_acceptance(approval_id,tenant_id,task_id,task_revision,push_id,pr_id)
        VALUES(p_approval,p_tenant,p_task,p_revision,p_push,p_pr);
        RETURN true;
    EXCEPTION WHEN SQLSTATE 'PBA01' OR unique_violation THEN
        RETURN false;
    END;
END $$;
REVOKE ALL ON FUNCTION browseai.accept_github_ci(uuid,uuid,bigint,uuid,uuid,uuid,bytea,uuid,bytea,bigint,text) FROM PUBLIC;
COMMIT;
