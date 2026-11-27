BEGIN;
-- Resolve the repository name only for an unexpired claim and current binding.
CREATE FUNCTION browseai.github_monitor_repository(p_id uuid,p_token uuid)
RETURNS text LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
    SELECT r.full_name FROM browseai.github_monitors m
    JOIN browseai.tasks t ON t.tenant_id=m.tenant_id AND t.id=m.task_id
    JOIN browseai.projects p ON p.tenant_id=m.tenant_id AND p.id=m.project_id
    JOIN browseai.tenants w ON w.id=m.tenant_id
    JOIN browseai.github_installations i ON i.tenant_id=m.tenant_id AND i.installation_id=m.installation_id
    JOIN browseai.github_repositories r ON r.tenant_id=m.tenant_id AND r.repository_id=m.repository_id AND r.installation_id=m.installation_id
    WHERE m.id=p_id AND m.claim_token=p_token AND m.claim_until>clock_timestamp()
      AND m.expires_at>clock_timestamp() AND t.state='WAIT_CI' AND t.revision=m.task_revision AND t.code_sha=m.code_sha
      AND w.enabled AND i.enabled AND r.enabled AND lower(p.repository)=lower(r.full_name)
      AND EXISTS(SELECT 1 FROM browseai.github_binding_audit a WHERE a.tenant_id=r.tenant_id AND a.installation_id=r.installation_id AND a.repository_id=r.repository_id)
$$;
REVOKE ALL ON FUNCTION browseai.github_monitor_repository(uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.github_monitor_repository(uuid,uuid) TO browseai_monitor;
COMMIT;
