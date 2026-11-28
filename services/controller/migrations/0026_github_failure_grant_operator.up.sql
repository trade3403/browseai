BEGIN;
GRANT USAGE ON SCHEMA browseai TO browseai_failure_grant_operator;
ALTER TABLE browseai.github_failure_observer_grants ADD COLUMN revoked_by text;

CREATE FUNCTION browseai.issue_github_failure_grant(p_id uuid,p_tenant uuid,p_task uuid,p_project uuid,p_revision bigint,
    p_installation bigint,p_repository bigint,p_branch text,p_sha text,p_workflow bigint,p_source text,p_event text,p_expires timestamptz)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE n timestamptz;
BEGIN
    IF session_user<>'browseai_failure_grant_operator' OR p_id IS NULL OR p_revision IS NULL OR p_revision<0
       OR p_workflow IS NULL OR p_workflow<=0 OR p_source IS NULL OR p_source !~ '^[0-9a-f]{64}$'
       OR p_event IS NULL OR p_event NOT IN ('push','pull_request') OR p_expires IS NULL OR NOT isfinite(p_expires)
       THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.tasks WHERE tenant_id=p_tenant AND id=p_task AND project_id=p_project
      AND revision=p_revision AND state='WAIT_CI' AND code_sha=p_sha FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.projects p JOIN browseai.tenants w ON w.id=p.tenant_id
    JOIN browseai.github_repositories r ON r.tenant_id=p.tenant_id AND r.repository_id=p.repository_id AND lower(r.full_name)=lower(p.repository)
    JOIN browseai.github_installations i ON i.tenant_id=r.tenant_id AND i.installation_id=r.installation_id
    WHERE p.tenant_id=p_tenant AND p.id=p_project AND p.code_branch=p_branch AND r.repository_id=p_repository
      AND i.installation_id=p_installation AND w.enabled AND r.enabled AND i.enabled
      AND EXISTS(SELECT 1 FROM browseai.github_binding_audit a WHERE a.tenant_id=r.tenant_id AND a.installation_id=i.installation_id AND a.repository_id=r.repository_id)
    FOR SHARE OF p,w,r,i;
    IF NOT FOUND THEN RETURN false; END IF;
    n:=clock_timestamp();
    IF p_expires<=n OR p_expires>n+interval '24 hours' THEN RETURN false; END IF;
    INSERT INTO browseai.github_failure_observer_grants(id,tenant_id,task_id,project_id,task_revision,installation_id,repository_id,
      code_branch,head_sha,workflow_id,source_digest,event_type,approved_by,created_at,expires_at)
    VALUES(p_id,p_tenant,p_task,p_project,p_revision,p_installation,p_repository,p_branch,p_sha,p_workflow,p_source,p_event,session_user,n,p_expires)
    ON CONFLICT DO NOTHING;
    RETURN FOUND;
END $$;

CREATE FUNCTION browseai.revoke_github_failure_grant(p_id uuid,p_tenant uuid)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF session_user<>'browseai_failure_grant_operator' THEN RETURN false; END IF;
    UPDATE browseai.github_failure_observer_grants SET revoked_at=clock_timestamp(),revoked_by=session_user
    WHERE id=p_id AND tenant_id=p_tenant AND revoked_at IS NULL AND failure_id IS NULL;
    RETURN FOUND;
END $$;
REVOKE ALL ON FUNCTION browseai.issue_github_failure_grant(uuid,uuid,uuid,uuid,bigint,bigint,bigint,text,text,bigint,text,text,timestamptz),
    browseai.revoke_github_failure_grant(uuid,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.issue_github_failure_grant(uuid,uuid,uuid,uuid,bigint,bigint,bigint,text,text,bigint,text,text,timestamptz),
    browseai.revoke_github_failure_grant(uuid,uuid) TO browseai_failure_grant_operator;
COMMIT;
