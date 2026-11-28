BEGIN;
-- Historical accepted evidence only; no raw proof or session token is exposed.
CREATE FUNCTION browseai.read_github_stage_handoff(p_digest bytea,p_tenant uuid,p_task uuid)
RETURNS TABLE(acceptance_id uuid,revision bigint,stage_index integer,stage_count integer,head_sha text,workflow_id bigint,push_id uuid,pr_id uuid,push_digest text,pr_digest text,source_digest text)
LANGUAGE plpgsql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE t browseai.tasks%ROWTYPE; actor uuid;
BEGIN
    IF session_user<>'browseai_stage_human' OR p_digest IS NULL OR octet_length(p_digest)<>32 THEN RETURN; END IF;
    SELECT s.user_id INTO actor FROM browseai.auth_sessions s
    JOIN browseai.users u ON u.id=s.user_id JOIN browseai.tenants w ON w.id=s.tenant_id
    JOIN browseai.memberships m ON m.user_id=s.user_id AND m.tenant_id=s.tenant_id
    WHERE s.token_digest=p_digest AND s.tenant_id=p_tenant AND s.revoked_at IS NULL
      AND s.expires_at>statement_timestamp() AND u.enabled AND w.enabled AND m.role='platform_admin';
    IF NOT FOUND THEN RETURN; END IF;
    SELECT * INTO t FROM browseai.tasks x WHERE x.tenant_id=p_tenant AND x.id=p_task AND x.state='STAGE_PASSED';
    IF NOT FOUND THEN RETURN; END IF;
    RETURN QUERY
    SELECT c.approval_id,t.revision,t.stage_index,t.stage_count,t.code_sha::text,e.workflow_id,e.id,q.id,encode(e.evidence_digest,'hex'),encode(q.evidence_digest,'hex'),e.source_digest::text
    FROM browseai.github_ci_acceptance c
    JOIN browseai.github_ci_evidence e ON e.id=c.push_id
    JOIN browseai.github_ci_evidence q ON q.id=c.pr_id
    JOIN browseai.projects p ON p.tenant_id=c.tenant_id AND p.id=t.project_id
    JOIN browseai.github_repositories r ON r.tenant_id=p.tenant_id AND r.repository_id=p.repository_id AND lower(r.full_name)=lower(p.repository)
    JOIN browseai.github_installations i ON i.tenant_id=r.tenant_id AND i.installation_id=r.installation_id
    JOIN browseai.github_verifier_keys v ON v.key_id=e.key_id
    JOIN browseai.github_verifier_keys z ON z.key_id=q.key_id
    JOIN browseai.audit_logs a ON a.id=c.approval_id
    WHERE c.tenant_id=p_tenant AND c.task_id=p_task AND c.task_revision=t.revision-1
      AND a.tenant_id=p_tenant AND a.resource_id=p_task AND a.action='task.ci_pass' AND a.revision=t.revision
      AND e.tenant_id=p_tenant AND q.tenant_id=p_tenant AND e.task_id=p_task AND q.task_id=p_task
      AND e.task_revision=c.task_revision AND q.task_revision=c.task_revision
      AND e.event_type='push' AND q.event_type='pull_request'
      AND e.consumed_at IS NOT NULL AND q.consumed_at IS NOT NULL
      AND e.head_sha=t.code_sha AND q.head_sha=t.code_sha AND e.project_id=p.id AND q.project_id=p.id
      AND e.repository_id=r.repository_id AND q.repository_id=r.repository_id
      AND e.installation_id=i.installation_id AND q.installation_id=i.installation_id
      AND e.code_branch=p.code_branch AND q.code_branch=p.code_branch AND r.enabled AND i.enabled
      AND v.revoked_at IS NULL AND z.revoked_at IS NULL
      AND EXISTS(SELECT 1 FROM browseai.github_binding_audit b WHERE b.tenant_id=r.tenant_id AND b.installation_id=i.installation_id AND b.repository_id=r.repository_id)
      AND NOT EXISTS(SELECT 1 FROM browseai.github_stage_decisions d WHERE d.acceptance_id=c.approval_id)
    LIMIT 1;
END $$;
REVOKE ALL ON FUNCTION browseai.read_github_stage_handoff(bytea,uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.read_github_stage_handoff(bytea,uuid,uuid) TO browseai_stage_human;
COMMIT;
