BEGIN;
-- Read-only presentation of current observation, not verified CI evidence.
-- No direct monitor table privilege or task write authority is granted.
CREATE FUNCTION browseai.github_task_ci_status(p_task uuid)
RETURNS TABLE(workflow_id bigint,event_type text,state text,observed_at timestamptz,next_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
    SELECT m.workflow_id,m.event_type,
        CASE WHEN m.claim_token IS NULL AND m.observed_at<=statement_timestamp()
          AND m.observed_at>statement_timestamp()-interval '60 seconds'
          AND m.expires_at>statement_timestamp() AND m.failures=0
          THEN m.state ELSE 'unknown' END,
        CASE WHEN m.claim_token IS NULL AND m.observed_at<=statement_timestamp() THEN m.observed_at ELSE NULL END,
        m.next_at
    FROM browseai.github_monitors m
    JOIN browseai.tasks t ON t.tenant_id=m.tenant_id AND t.id=m.task_id AND t.project_id=m.project_id
    JOIN browseai.projects p ON p.tenant_id=t.tenant_id AND p.id=t.project_id
    JOIN browseai.tenants w ON w.id=t.tenant_id
    JOIN browseai.github_repositories r ON r.tenant_id=p.tenant_id AND r.repository_id=p.repository_id AND lower(r.full_name)=lower(p.repository)
    JOIN browseai.github_installations i ON i.tenant_id=r.tenant_id AND i.installation_id=r.installation_id
    JOIN browseai.memberships u ON u.tenant_id=t.tenant_id AND u.user_id=nullif(current_setting('browseai.actor_id',true),'')::uuid
    JOIN browseai.users a ON a.id=u.user_id
    WHERE session_user='browseai_app' AND t.tenant_id=browseai.current_tenant() AND t.id=p_task
      AND u.role='user' AND a.enabled AND w.enabled AND r.enabled AND i.enabled
      AND t.state='WAIT_CI' AND t.revision=m.task_revision AND t.code_sha=m.code_sha
      AND p.repository_id=m.repository_id AND p.code_branch=m.code_branch AND i.installation_id=m.installation_id
      AND EXISTS(SELECT 1 FROM browseai.github_binding_audit b WHERE b.tenant_id=r.tenant_id AND b.installation_id=i.installation_id AND b.repository_id=r.repository_id)
    ORDER BY m.workflow_id,m.event_type LIMIT 101
$$;
REVOKE ALL ON FUNCTION browseai.github_task_ci_status(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.github_task_ci_status(uuid) TO browseai_app;
COMMIT;
