BEGIN;
-- Trusted monitor policy supplies the scope and workflow. Derive task SHA and
-- revision from locked rows. Repeated scheduling never refreshes expiry.
CREATE FUNCTION browseai.schedule_github_monitor_pair(p_tenant uuid,p_installation bigint,p_repository bigint,p_workflow bigint)
RETURNS integer LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE candidate record; inserted integer; total integer:=0;
BEGIN
    IF p_tenant IS NULL OR p_installation IS NULL OR p_installation<=0 OR p_repository IS NULL OR p_repository<=0
       OR p_workflow IS NULL OR p_workflow<=0 THEN RETURN 0; END IF;
    FOR candidate IN
        SELECT t.id,t.project_id,t.revision,t.code_sha,p.code_branch
        FROM browseai.tasks t JOIN browseai.projects p ON p.tenant_id=t.tenant_id AND p.id=t.project_id
        JOIN browseai.tenants w ON w.id=t.tenant_id
        JOIN browseai.github_repositories r ON r.tenant_id=p.tenant_id AND r.repository_id=p.repository_id AND lower(r.full_name)=lower(p.repository)
        JOIN browseai.github_installations i ON i.tenant_id=r.tenant_id AND i.installation_id=r.installation_id
        WHERE t.tenant_id=p_tenant AND t.state='WAIT_CI' AND p.repository_id=p_repository
          AND i.installation_id=p_installation AND w.enabled AND r.enabled AND i.enabled
          AND EXISTS(SELECT 1 FROM browseai.github_binding_audit a WHERE a.tenant_id=r.tenant_id AND a.installation_id=i.installation_id AND a.repository_id=r.repository_id)
          AND (SELECT count(*) FROM browseai.github_monitors m WHERE m.task_id=t.id AND m.task_revision=t.revision
               AND m.workflow_id=p_workflow AND m.event_type IN ('push','pull_request'))<2
        ORDER BY t.id LIMIT 32 FOR UPDATE OF t SKIP LOCKED
    LOOP
        -- Revalidate live binding after acquiring the task lock.
        PERFORM 1 FROM browseai.projects p JOIN browseai.tenants w ON w.id=p.tenant_id
        JOIN browseai.github_repositories r ON r.tenant_id=p.tenant_id AND r.repository_id=p.repository_id AND lower(r.full_name)=lower(p.repository)
        JOIN browseai.github_installations i ON i.tenant_id=r.tenant_id AND i.installation_id=r.installation_id
        WHERE p.tenant_id=p_tenant AND p.id=candidate.project_id AND p.repository_id=p_repository
          AND p.code_branch=candidate.code_branch AND i.installation_id=p_installation AND w.enabled AND r.enabled AND i.enabled
          AND EXISTS(SELECT 1 FROM browseai.github_binding_audit a WHERE a.tenant_id=r.tenant_id AND a.installation_id=i.installation_id AND a.repository_id=r.repository_id)
        FOR SHARE OF p,w,r,i;
        IF NOT FOUND THEN CONTINUE; END IF;
        INSERT INTO browseai.github_monitors(tenant_id,task_id,project_id,task_revision,repository_id,installation_id,code_branch,code_sha,workflow_id,event_type,expires_at)
        SELECT p_tenant,candidate.id,candidate.project_id,candidate.revision,p_repository,p_installation,candidate.code_branch,candidate.code_sha,p_workflow,e,statement_timestamp()+interval '24 hours'
        FROM (VALUES('push'),('pull_request')) AS events(e)
        ON CONFLICT(task_id,task_revision,workflow_id,event_type) DO NOTHING;
        GET DIAGNOSTICS inserted=ROW_COUNT;
        total:=total+inserted;
    END LOOP;
    RETURN total;
END $$;
REVOKE ALL ON FUNCTION browseai.schedule_github_monitor_pair(uuid,bigint,bigint,bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.schedule_github_monitor_pair(uuid,bigint,bigint,bigint) TO browseai_monitor;
COMMIT;
