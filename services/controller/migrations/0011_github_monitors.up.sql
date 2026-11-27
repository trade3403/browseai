BEGIN;
GRANT USAGE ON SCHEMA browseai TO browseai_monitor;
CREATE TABLE browseai.github_monitors (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL,
    task_id uuid NOT NULL,
    project_id uuid NOT NULL,
    task_revision bigint NOT NULL CHECK(task_revision>=0),
    repository_id bigint NOT NULL,
    installation_id bigint NOT NULL,
    code_branch text NOT NULL,
    code_sha text NOT NULL CHECK(code_sha ~ '^[0-9a-f]{40}$'),
    workflow_id bigint NOT NULL CHECK(workflow_id>0),
    event_type text NOT NULL CHECK(event_type IN ('push','pull_request')),
    state text NOT NULL DEFAULT 'unknown' CHECK(state IN ('unknown','pending','failed','cancelled','passed')),
    failures integer NOT NULL DEFAULT 0 CHECK(failures BETWEEN 0 AND 8),
    next_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    observed_at timestamptz,
    expires_at timestamptz NOT NULL,
    claim_token uuid,
    claim_until timestamptz,
    UNIQUE(task_id,task_revision,workflow_id,event_type),
    FOREIGN KEY(tenant_id,task_id,project_id) REFERENCES browseai.tasks(tenant_id,id,project_id),
    FOREIGN KEY(tenant_id,project_id,repository_id,code_branch) REFERENCES browseai.projects(tenant_id,id,repository_id,code_branch),
    FOREIGN KEY(tenant_id,installation_id) REFERENCES browseai.github_installations(tenant_id,installation_id),
    FOREIGN KEY(tenant_id,repository_id) REFERENCES browseai.github_repositories(tenant_id,repository_id),
    CHECK((claim_token IS NULL)=(claim_until IS NULL))
);
CREATE INDEX github_monitors_due_idx ON browseai.github_monitors(next_at,id);
REVOKE ALL ON browseai.github_monitors FROM PUBLIC;
-- Only trusted scheduling code may create a target; derive all task identity
-- from current WAIT_CI state rather than accepting a caller-supplied SHA.
CREATE FUNCTION browseai.enqueue_github_monitor(p_tenant uuid,p_task uuid,p_workflow bigint,p_event text)
RETURNS uuid LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE result uuid;
BEGIN
    INSERT INTO browseai.github_monitors(tenant_id,task_id,project_id,task_revision,repository_id,installation_id,code_branch,code_sha,workflow_id,event_type,expires_at)
    SELECT t.tenant_id,t.id,p.id,t.revision,p.repository_id,r.installation_id,p.code_branch,t.code_sha,p_workflow,p_event,clock_timestamp()+interval '24 hours'
    FROM browseai.tasks t JOIN browseai.projects p ON p.tenant_id=t.tenant_id AND p.id=t.project_id
    JOIN browseai.tenants w ON w.id=t.tenant_id
    JOIN browseai.github_repositories r ON r.tenant_id=p.tenant_id AND r.repository_id=p.repository_id AND lower(r.full_name)=lower(p.repository)
    JOIN browseai.github_installations i ON i.tenant_id=r.tenant_id AND i.installation_id=r.installation_id
    WHERE t.tenant_id=p_tenant AND t.id=p_task AND t.state='WAIT_CI' AND w.enabled AND r.enabled AND i.enabled
    AND EXISTS(SELECT 1 FROM browseai.github_binding_audit a WHERE a.tenant_id=r.tenant_id AND a.installation_id=r.installation_id AND a.repository_id=r.repository_id)
    ON CONFLICT(task_id,task_revision,workflow_id,event_type) DO NOTHING RETURNING id INTO result;
    RETURN result;
END $$;
-- Durable leases are per target. Expired claims can be reclaimed, and the
-- old token can never publish after the new worker claims the target.
CREATE FUNCTION browseai.claim_github_monitor()
RETURNS SETOF browseai.github_monitors LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
    WITH candidate AS (
        SELECT m.id FROM browseai.github_monitors m
        JOIN browseai.tasks t ON t.tenant_id=m.tenant_id AND t.id=m.task_id
        JOIN browseai.tenants w ON w.id=m.tenant_id
        JOIN browseai.github_installations i ON i.tenant_id=m.tenant_id AND i.installation_id=m.installation_id
        JOIN browseai.github_repositories r ON r.tenant_id=m.tenant_id AND r.repository_id=m.repository_id AND r.installation_id=m.installation_id
        WHERE m.next_at<=clock_timestamp() AND m.expires_at>clock_timestamp() AND m.failures<8
          AND (m.claim_until IS NULL OR m.claim_until<=clock_timestamp())
          AND t.state='WAIT_CI' AND t.revision=m.task_revision AND t.code_sha=m.code_sha AND w.enabled AND i.enabled AND r.enabled
        ORDER BY m.next_at,m.id LIMIT 1 FOR UPDATE OF m SKIP LOCKED
    )
    UPDATE browseai.github_monitors m SET claim_token=gen_random_uuid(),claim_until=clock_timestamp()+interval '120 seconds',
        state='unknown',observed_at=NULL
    FROM candidate c WHERE m.id=c.id RETURNING m.*
$$;
CREATE FUNCTION browseai.publish_github_monitor(p_id uuid,p_token uuid,p_state text,p_failures integer,p_next timestamptz)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    UPDATE browseai.github_monitors m SET state=p_state,failures=p_failures,next_at=p_next,
        observed_at=clock_timestamp(),claim_token=NULL,claim_until=NULL
    WHERE m.id=p_id AND m.claim_token=p_token AND m.claim_until>clock_timestamp() AND m.expires_at>clock_timestamp()
      AND p_next>=clock_timestamp() AND p_failures BETWEEN 0 AND 8
      AND p_state IN ('unknown','pending','failed','cancelled','passed')
      AND ((p_state='unknown' AND p_failures>0) OR (p_state<>'unknown' AND p_failures=0))
      AND EXISTS(SELECT 1 FROM browseai.tasks t JOIN browseai.tenants w ON w.id=t.tenant_id
          JOIN browseai.github_installations i ON i.tenant_id=t.tenant_id AND i.installation_id=m.installation_id
          JOIN browseai.github_repositories r ON r.tenant_id=t.tenant_id AND r.repository_id=m.repository_id AND r.installation_id=i.installation_id
          WHERE t.tenant_id=m.tenant_id AND t.id=m.task_id AND t.state='WAIT_CI' AND t.revision=m.task_revision AND t.code_sha=m.code_sha
          AND w.enabled AND i.enabled AND r.enabled);
    RETURN FOUND;
END $$;
REVOKE ALL ON FUNCTION browseai.enqueue_github_monitor(uuid,uuid,bigint,text),browseai.claim_github_monitor(),browseai.publish_github_monitor(uuid,uuid,text,integer,timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.enqueue_github_monitor(uuid,uuid,bigint,text),browseai.claim_github_monitor(),browseai.publish_github_monitor(uuid,uuid,text,integer,timestamptz) TO browseai_monitor;
COMMIT;
