BEGIN;
-- Reserved failure evidence only; no raw proof or session token is exposed.
CREATE FUNCTION browseai.read_github_repair_handoff(p_digest bytea,p_tenant uuid,p_task uuid)
RETURNS TABLE(grant_id uuid,failure_id uuid,revision bigint,stage_index integer,head_sha text,workflow_id bigint,event_type text,fence bigint,approval_id uuid,instruction_digest text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE t browseai.tasks%ROWTYPE; actor uuid;
BEGIN
    IF session_user<>'browseai_repair_human' OR p_digest IS NULL OR octet_length(p_digest)<>32 THEN RETURN; END IF;
    SELECT s.user_id INTO actor FROM browseai.auth_sessions s
    JOIN browseai.users u ON u.id=s.user_id JOIN browseai.tenants w ON w.id=s.tenant_id
    JOIN browseai.memberships m ON m.user_id=s.user_id AND m.tenant_id=s.tenant_id
    WHERE s.token_digest=p_digest AND s.tenant_id=p_tenant AND s.revoked_at IS NULL
      AND s.expires_at>statement_timestamp() AND u.enabled AND w.enabled AND m.role='platform_admin';
    IF NOT FOUND THEN RETURN; END IF;
    SELECT * INTO t FROM browseai.tasks x WHERE x.tenant_id=p_tenant AND x.id=p_task AND x.state='AWAIT_APPROVAL' AND x.repairs>0;
    IF NOT FOUND THEN RETURN; END IF;
    RETURN QUERY
    SELECT g.id,f.id,t.revision,t.stage_index,t.code_sha::text,f.workflow_id,f.event_type::text,l.fence,
      ap.approval_id,encode(ap.instruction_digest,'hex')
    FROM browseai.github_failure_observer_grants g
    JOIN browseai.github_ci_failures f ON f.id=g.failure_id
    JOIN browseai.github_ci_repair_audit a ON a.failure_id=f.id
    JOIN browseai.leases l ON l.tenant_id=t.tenant_id AND l.task_id=t.id AND l.project_id=t.project_id
    JOIN browseai.projects p ON p.tenant_id=t.tenant_id AND p.id=t.project_id
    JOIN browseai.github_repositories r ON r.tenant_id=p.tenant_id AND r.repository_id=p.repository_id AND lower(r.full_name)=lower(p.repository)
    JOIN browseai.github_installations i ON i.tenant_id=r.tenant_id AND i.installation_id=r.installation_id
    LEFT JOIN browseai.github_ci_repair_approvals ap ON ap.failure_id=f.id
    LEFT JOIN browseai.audit_logs al ON al.id=ap.approval_id
    WHERE g.tenant_id=p_tenant AND g.task_id=p_task AND g.task_revision=t.revision-2
      AND g.revoked_at IS NULL AND f.tenant_id=g.tenant_id AND f.task_id=g.task_id
      AND f.task_revision=g.task_revision AND f.project_id=g.project_id AND f.project_id=t.project_id
      AND f.code_branch=g.code_branch AND f.repository_id=g.repository_id AND f.installation_id=g.installation_id
      AND f.head_sha=g.head_sha AND f.head_sha=t.code_sha AND f.workflow_id=g.workflow_id
      AND f.source_digest=g.source_digest AND f.event_type=g.event_type
      AND f.created_at>=g.created_at AND f.created_at<g.expires_at AND f.repair_at IS NOT NULL
      AND a.action='repair_reserved' AND a.task_revision=t.revision AND a.repairs=t.repairs
      AND a.observer_role='browseai_repair_reserver'
      AND l.expires_at>statement_timestamp() AND l.code_branch=p.code_branch AND l.repository_id=r.repository_id
      AND f.code_branch=p.code_branch AND f.repository_id=r.repository_id AND f.installation_id=i.installation_id
      AND r.enabled AND i.enabled
      AND EXISTS(SELECT 1 FROM browseai.github_binding_audit b WHERE b.tenant_id=r.tenant_id AND b.installation_id=i.installation_id AND b.repository_id=r.repository_id)
      AND (ap.approval_id IS NULL OR (ap.tenant_id=p_tenant AND ap.task_id=p_task AND ap.task_revision=t.revision
        AND ap.stage_index=t.stage_index AND ap.fence=l.fence AND al.actor_id=actor AND al.tenant_id=p_tenant
        AND al.resource_id=p_task AND al.revision=t.revision AND al.action='task.ci_repair_approve'))
    LIMIT 1;
END $$;
REVOKE ALL ON FUNCTION browseai.read_github_repair_handoff(bytea,uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.read_github_repair_handoff(bytea,uuid,uuid) TO browseai_repair_human;
COMMIT;
