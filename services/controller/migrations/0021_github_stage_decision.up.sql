BEGIN;
-- A separate, explicit stage decision consumes an accepted CI result once.
-- No runtime role can call this owner-only primitive.
CREATE TABLE browseai.github_stage_decisions (
    decision_id uuid PRIMARY KEY REFERENCES browseai.audit_logs(id),
    acceptance_id uuid NOT NULL UNIQUE REFERENCES browseai.github_ci_acceptance(approval_id),
    tenant_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_revision bigint NOT NULL CHECK (task_revision>=0),
    stage_index integer NOT NULL CHECK (stage_index BETWEEN 0 AND 99),
    decision text NOT NULL CHECK (decision IN ('next_stage','complete')),
    UNIQUE(tenant_id,task_id,task_revision),
    FOREIGN KEY(tenant_id,task_id) REFERENCES browseai.tasks(tenant_id,id)
);
REVOKE ALL ON browseai.github_stage_decisions FROM PUBLIC;
ALTER TABLE browseai.github_stage_decisions ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.github_stage_decisions FORCE ROW LEVEL SECURITY;

CREATE FUNCTION browseai.decide_github_stage(p_tenant uuid,p_task uuid,p_revision bigint,p_acceptance uuid,
    p_actor uuid,p_decision_id uuid,p_decision text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE t browseai.tasks%ROWTYPE; target text; action_name text;
BEGIN
    IF p_revision IS NULL OR p_revision<=0 OR p_revision=9223372036854775807
       OR p_actor IS NULL OR p_decision_id IS NULL OR p_acceptance IS NULL OR p_decision_id=p_acceptance
       OR p_decision IS NULL OR p_decision NOT IN ('next_stage','complete') THEN RETURN false; END IF;
    SELECT * INTO t FROM browseai.tasks WHERE tenant_id=p_tenant AND id=p_task AND revision=p_revision
      AND state='STAGE_PASSED' FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    IF p_decision='next_stage' AND t.stage_index+1<t.stage_count THEN
        target:='QUEUED'; action_name:='task.next_stage';
    ELSIF p_decision='complete' AND t.stage_index+1=t.stage_count THEN
        target:='COMPLETED'; action_name:='task.complete';
    ELSE RETURN false;
    END IF;
    PERFORM 1 FROM browseai.memberships m JOIN browseai.users u ON u.id=m.user_id
    JOIN browseai.tenants w ON w.id=m.tenant_id
    WHERE m.tenant_id=p_tenant AND m.user_id=p_actor AND m.role='platform_admin'
      AND u.enabled AND w.enabled FOR SHARE OF m,u,w;
    IF NOT FOUND THEN RETURN false; END IF;
    -- Historical consumption is not renewed: require exact committed acceptance,
    -- matching task/head, consumed pair and current binding/authorization.
    PERFORM 1 FROM browseai.github_ci_acceptance c
    JOIN browseai.github_ci_evidence e ON e.id=c.push_id
    JOIN browseai.github_ci_evidence q ON q.id=c.pr_id
    JOIN browseai.projects p ON p.tenant_id=c.tenant_id AND p.id=t.project_id
    JOIN browseai.github_repositories r ON r.tenant_id=p.tenant_id AND r.repository_id=p.repository_id AND lower(r.full_name)=lower(p.repository)
    JOIN browseai.github_installations i ON i.tenant_id=r.tenant_id AND i.installation_id=r.installation_id
    JOIN browseai.github_verifier_keys v ON v.key_id=e.key_id
    JOIN browseai.github_verifier_keys z ON z.key_id=q.key_id
    JOIN browseai.audit_logs a ON a.id=c.approval_id
    WHERE c.approval_id=p_acceptance AND c.tenant_id=p_tenant AND c.task_id=p_task AND c.task_revision=p_revision-1
      AND a.tenant_id=p_tenant AND a.resource_id=p_task AND a.action='task.ci_pass' AND a.revision=p_revision
      AND e.tenant_id=p_tenant AND q.tenant_id=p_tenant AND e.task_id=p_task AND q.task_id=p_task
      AND e.task_revision=c.task_revision AND q.task_revision=c.task_revision
      AND e.event_type='push' AND q.event_type='pull_request'
      AND e.consumed_at IS NOT NULL AND q.consumed_at IS NOT NULL
      AND e.head_sha=t.code_sha AND q.head_sha=t.code_sha AND e.project_id=p.id AND q.project_id=p.id
      AND e.repository_id=r.repository_id AND q.repository_id=r.repository_id
      AND e.installation_id=i.installation_id AND q.installation_id=i.installation_id
      AND e.code_branch=p.code_branch AND q.code_branch=p.code_branch AND r.enabled AND i.enabled
      AND v.revoked_at IS NULL AND z.revoked_at IS NULL
    FOR SHARE OF c,e,q,p,r,i,v,z,a;
    IF NOT FOUND THEN RETURN false; END IF;
    -- The Go adapter validates the entire stage plan; SQL additionally
    -- rejects incomplete or missing stage rows while the task is locked.
    IF (SELECT count(*) FROM browseai.stages s WHERE s.tenant_id=p_tenant AND s.task_id=p_task
        AND s.ordinal<t.stage_count AND s.instruction IS NOT NULL AND cardinality(s.acceptance)>0)<>t.stage_count
        OR (SELECT count(*) FROM browseai.stages s WHERE s.tenant_id=p_tenant AND s.task_id=p_task)<>t.stage_count
        THEN RETURN false; END IF;
    BEGIN
        UPDATE browseai.tasks SET state=target,revision=revision+1,updated_at=clock_timestamp(),
            stage_index=stage_index+CASE WHEN target='QUEUED' THEN 1 ELSE 0 END,
            repairs=CASE WHEN target='QUEUED' THEN 0 ELSE repairs END
        WHERE tenant_id=p_tenant AND id=p_task AND revision=p_revision AND state='STAGE_PASSED';
        IF NOT FOUND THEN RAISE EXCEPTION 'stage decision lost'; END IF;
        INSERT INTO browseai.audit_logs(id,tenant_id,actor_id,action,resource_id,revision)
        VALUES(p_decision_id,p_tenant,p_actor,action_name,p_task,p_revision+1);
        INSERT INTO browseai.events(tenant_id,audit_id,kind,resource_id,revision)
        VALUES(p_tenant,p_decision_id,action_name,p_task,p_revision+1);
        INSERT INTO browseai.github_stage_decisions(decision_id,acceptance_id,tenant_id,task_id,task_revision,stage_index,decision)
        VALUES(p_decision_id,p_acceptance,p_tenant,p_task,p_revision,t.stage_index,p_decision);
        RETURN true;
    EXCEPTION WHEN unique_violation THEN RETURN false;
    END;
END $$;
REVOKE ALL ON FUNCTION browseai.decide_github_stage(uuid,uuid,bigint,uuid,uuid,uuid,text) FROM PUBLIC;
COMMIT;
