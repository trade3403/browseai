BEGIN;
-- Explicit human reported send, not browser telemetry or automatic delivery.
-- The caller must authenticate the human and reconcile the observed send.
CREATE TABLE browseai.github_ci_repair_confirmations (
    confirmation_id uuid PRIMARY KEY REFERENCES browseai.audit_logs(id),
    approval_id uuid NOT NULL UNIQUE REFERENCES browseai.github_ci_repair_approvals(approval_id),
    tenant_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_revision bigint NOT NULL CHECK (task_revision>=0),
    fence bigint NOT NULL CHECK (fence>0),
    instruction_digest bytea NOT NULL CHECK (octet_length(instruction_digest)=32),
    confirmed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(tenant_id,task_id,task_revision),
    FOREIGN KEY(tenant_id,task_id) REFERENCES browseai.tasks(tenant_id,id)
);
REVOKE ALL ON browseai.github_ci_repair_confirmations FROM PUBLIC;
ALTER TABLE browseai.github_ci_repair_confirmations ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.github_ci_repair_confirmations FORCE ROW LEVEL SECURITY;

CREATE FUNCTION browseai.confirm_github_ci_repair(p_tenant uuid,p_task uuid,p_revision bigint,p_approval uuid,
    p_actor uuid,p_confirmation uuid,p_fence bigint,p_instruction bytea)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE t browseai.tasks%ROWTYPE; digest bytea; lease_until timestamptz; n timestamptz;
BEGIN
    IF p_revision IS NULL OR p_revision<2 OR p_revision=9223372036854775807 OR p_fence IS NULL OR p_fence<=0
       OR p_actor IS NULL OR p_approval IS NULL OR p_confirmation IS NULL OR p_confirmation=p_approval
       OR p_instruction IS NULL OR octet_length(p_instruction) NOT BETWEEN 1 AND 16384 THEN RETURN false; END IF;
    digest:=sha256(p_instruction);
    SELECT * INTO t FROM browseai.tasks WHERE tenant_id=p_tenant AND id=p_task AND revision=p_revision
      AND state='AWAIT_APPROVAL' AND repairs>0 FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.github_ci_repair_approvals p
    JOIN browseai.github_ci_failures f ON f.id=p.failure_id
    JOIN browseai.github_ci_repair_audit h ON h.failure_id=f.id
    JOIN browseai.audit_logs a ON a.id=p.approval_id
    WHERE p.approval_id=p_approval AND p.tenant_id=p_tenant AND p.task_id=p_task AND p.task_revision=p_revision
      AND p.stage_index=t.stage_index AND p.fence=p_fence AND p.instruction_digest=digest
      AND f.tenant_id=p_tenant AND f.task_id=p_task AND f.task_revision=p_revision-2
      AND f.head_sha=t.code_sha AND f.repair_at IS NOT NULL
      AND h.action='repair_reserved' AND h.task_revision=p_revision AND h.repairs=t.repairs
      AND a.actor_id=p_actor AND a.tenant_id=p_tenant AND a.resource_id=p_task
      AND a.revision=p_revision AND a.action='task.ci_repair_approve'
    FOR SHARE OF p,f,h,a;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.memberships m JOIN browseai.users u ON u.id=m.user_id
    JOIN browseai.tenants w ON w.id=m.tenant_id
    WHERE m.tenant_id=p_tenant AND m.user_id=p_actor AND m.role='platform_admin'
      AND u.enabled AND w.enabled FOR SHARE OF m,u,w;
    IF NOT FOUND THEN RETURN false; END IF;
    SELECT l.expires_at INTO lease_until FROM browseai.leases l
    JOIN browseai.projects p ON p.tenant_id=l.tenant_id AND p.id=l.project_id
    JOIN browseai.github_repositories r ON r.tenant_id=p.tenant_id AND r.repository_id=p.repository_id AND lower(r.full_name)=lower(p.repository)
    JOIN browseai.github_installations i ON i.tenant_id=r.tenant_id AND i.installation_id=r.installation_id
    JOIN browseai.github_ci_repair_approvals a ON a.approval_id=p_approval
    JOIN browseai.github_ci_failures f ON f.id=a.failure_id AND f.repository_id=r.repository_id AND f.installation_id=i.installation_id
    WHERE l.tenant_id=p_tenant AND l.task_id=p_task AND l.project_id=t.project_id AND l.fence=p_fence
      AND l.expires_at>clock_timestamp() AND r.enabled AND i.enabled
      AND EXISTS(SELECT 1 FROM browseai.github_binding_audit b WHERE b.tenant_id=r.tenant_id AND b.installation_id=i.installation_id AND b.repository_id=r.repository_id)
    FOR SHARE OF l,p,r,i;
    IF NOT FOUND THEN RETURN false; END IF;
    n:=clock_timestamp();
    IF n>=lease_until THEN RETURN false; END IF;
    BEGIN
        UPDATE browseai.tasks SET state='DEVELOPING',revision=revision+1,updated_at=n
        WHERE tenant_id=p_tenant AND id=p_task AND revision=p_revision AND state='AWAIT_APPROVAL';
        IF NOT FOUND THEN RAISE EXCEPTION 'repair confirmation lost'; END IF;
        INSERT INTO browseai.audit_logs(id,tenant_id,actor_id,action,resource_id,revision)
        VALUES(p_confirmation,p_tenant,p_actor,'task.ci_repair_confirm_sent',p_task,p_revision+1);
        INSERT INTO browseai.events(tenant_id,audit_id,kind,resource_id,revision)
        VALUES(p_tenant,p_confirmation,'task.ci_repair_confirm_sent',p_task,p_revision+1);
        INSERT INTO browseai.github_ci_repair_confirmations(confirmation_id,approval_id,tenant_id,task_id,task_revision,fence,instruction_digest,confirmed_at)
        VALUES(p_confirmation,p_approval,p_tenant,p_task,p_revision,p_fence,digest,n);
        RETURN true;
    EXCEPTION WHEN unique_violation THEN RETURN false;
    END;
END $$;
REVOKE ALL ON FUNCTION browseai.confirm_github_ci_repair(uuid,uuid,bigint,uuid,uuid,uuid,bigint,bytea) FROM PUBLIC;
COMMIT;
