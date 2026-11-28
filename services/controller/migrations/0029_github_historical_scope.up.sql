BEGIN;
-- Historical rows without a provable original branch stay unscoped.
-- Do not reconstruct history from mutable current project settings.
ALTER TABLE browseai.github_ci_failures ADD COLUMN project_id uuid, ADD COLUMN code_branch text;
ALTER TABLE browseai.github_ci_failures ADD CONSTRAINT github_failure_scope_pair
    CHECK ((project_id IS NULL AND code_branch IS NULL) OR (project_id IS NOT NULL AND code_branch IS NOT NULL AND length(code_branch) BETWEEN 1 AND 255));
ALTER TABLE browseai.github_ci_failures ADD CONSTRAINT github_failure_project_fk
    FOREIGN KEY(tenant_id,task_id,project_id) REFERENCES browseai.tasks(tenant_id,id,project_id);

CREATE OR REPLACE FUNCTION browseai.record_github_ci_failure(p_tenant uuid,p_task uuid,p_revision bigint,p_project uuid,
    p_installation bigint,p_repository bigint,p_branch text,p_sha text,p_workflow bigint,p_source text,
    p_event text,p_run bigint,p_attempt bigint,p_jobs bigint[],p_observed timestamptz)
RETURNS uuid LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE n timestamptz; result uuid;
BEGIN
    IF p_revision IS NULL OR p_revision<0 OR p_revision=9223372036854775807
       OR p_observed IS NULL OR NOT isfinite(p_observed) OR p_jobs IS NULL
       OR cardinality(p_jobs) NOT BETWEEN 1 AND 100 OR array_ndims(p_jobs)<>1
       OR array_position(p_jobs,NULL) IS NOT NULL OR NOT (0<ALL(p_jobs))
       OR cardinality(p_jobs)<>(SELECT count(DISTINCT x) FROM unnest(p_jobs) AS j(x))
       THEN RETURN NULL; END IF;
    PERFORM 1 FROM browseai.tasks WHERE tenant_id=p_tenant AND id=p_task AND revision=p_revision
      AND state='WAIT_CI' AND project_id=p_project AND code_sha=p_sha FOR UPDATE;
    IF NOT FOUND THEN RETURN NULL; END IF;
    PERFORM 1 FROM browseai.projects p JOIN browseai.tenants t ON t.id=p.tenant_id
    JOIN browseai.github_repositories r ON r.tenant_id=p.tenant_id AND r.repository_id=p.repository_id AND lower(r.full_name)=lower(p.repository)
    JOIN browseai.github_installations i ON i.tenant_id=r.tenant_id AND i.installation_id=r.installation_id
    WHERE p.tenant_id=p_tenant AND p.id=p_project AND p.code_branch=p_branch AND r.repository_id=p_repository
      AND i.installation_id=p_installation AND t.enabled AND r.enabled AND i.enabled
      AND EXISTS(SELECT 1 FROM browseai.github_binding_audit a WHERE a.tenant_id=r.tenant_id AND a.installation_id=i.installation_id AND a.repository_id=r.repository_id)
    FOR SHARE OF p,t,r,i;
    IF NOT FOUND THEN RETURN NULL; END IF;
    n:=clock_timestamp();
    IF n<p_observed OR n>=p_observed+interval '30 seconds' THEN RETURN NULL; END IF;
    INSERT INTO browseai.github_ci_failures(tenant_id,task_id,task_revision,project_id,code_branch,repository_id,installation_id,workflow_id,event_type,run_id,attempt,head_sha,source_digest,failed_jobs,observed_at,created_at)
    VALUES(p_tenant,p_task,p_revision,p_project,p_branch,p_repository,p_installation,p_workflow,p_event,p_run,p_attempt,p_sha,p_source,p_jobs,p_observed,n)
    ON CONFLICT DO NOTHING RETURNING id INTO result;
    IF result IS NULL THEN RETURN NULL; END IF;
    UPDATE browseai.tasks SET state='CI_FAILED',revision=revision+1,updated_at=n
    WHERE tenant_id=p_tenant AND id=p_task AND revision=p_revision AND state='WAIT_CI';
    IF NOT FOUND THEN RAISE EXCEPTION 'failure transition lost'; END IF;
    RETURN result;
END $$;

CREATE OR REPLACE FUNCTION browseai.reserve_github_ci_repair(p_tenant uuid,p_task uuid,p_revision bigint,p_failure uuid,p_fence bigint)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE task_row browseai.tasks%ROWTYPE; failure_row browseai.github_ci_failures%ROWTYPE; n timestamptz; target text;
BEGIN
    IF p_revision IS NULL OR p_revision<=0 OR p_revision=9223372036854775807 OR p_fence IS NULL OR p_fence<=0 THEN RETURN NULL; END IF;
    SELECT * INTO task_row FROM browseai.tasks WHERE tenant_id=p_tenant AND id=p_task
      AND revision=p_revision AND state='CI_FAILED' FOR UPDATE;
    IF NOT FOUND THEN RETURN NULL; END IF;
    SELECT * INTO failure_row FROM browseai.github_ci_failures WHERE tenant_id=p_tenant AND task_id=p_task
      AND id=p_failure AND task_revision=p_revision-1 AND head_sha=task_row.code_sha AND repair_at IS NULL FOR UPDATE;
    IF NOT FOUND THEN RETURN NULL; END IF;
    PERFORM 1 FROM browseai.projects p JOIN browseai.tenants t ON t.id=p.tenant_id
    JOIN browseai.github_repositories r ON r.tenant_id=p.tenant_id AND r.repository_id=p.repository_id AND lower(r.full_name)=lower(p.repository)
    JOIN browseai.github_installations i ON i.tenant_id=r.tenant_id AND i.installation_id=r.installation_id
    JOIN browseai.leases l ON l.tenant_id=p.tenant_id AND l.project_id=p.id AND l.repository_id=r.repository_id AND l.code_branch=p.code_branch
    WHERE p.tenant_id=p_tenant AND p.id=task_row.project_id AND l.task_id=p_task AND l.fence=p_fence
      AND l.expires_at>clock_timestamp() AND failure_row.project_id=p.id AND failure_row.code_branch=p.code_branch
      AND r.repository_id=failure_row.repository_id AND i.installation_id=failure_row.installation_id
      AND t.enabled AND r.enabled AND i.enabled
      AND EXISTS(SELECT 1 FROM browseai.github_binding_audit b WHERE b.tenant_id=r.tenant_id AND b.installation_id=i.installation_id AND b.repository_id=r.repository_id)
      FOR SHARE OF p,t,r,i,l;
    IF NOT FOUND THEN RETURN NULL; END IF;
    n:=clock_timestamp();
    IF task_row.repairs>=task_row.max_repairs THEN target:='NEED_HUMAN'; ELSE target:='AWAIT_APPROVAL'; END IF;
    UPDATE browseai.tasks SET state=target,revision=revision+1,updated_at=n,
        suspended_state=CASE WHEN target='NEED_HUMAN' THEN 'CI_FAILED' ELSE NULL END,
        repairs=repairs+CASE WHEN target='AWAIT_APPROVAL' THEN 1 ELSE 0 END
    WHERE tenant_id=p_tenant AND id=p_task AND revision=p_revision AND state='CI_FAILED';
    IF NOT FOUND THEN RAISE EXCEPTION 'repair transition lost'; END IF;
    UPDATE browseai.github_ci_failures SET repair_at=n WHERE id=p_failure AND repair_at IS NULL;
    IF NOT FOUND THEN RAISE EXCEPTION 'repair reservation lost'; END IF;
    RETURN target;
END $$;

CREATE OR REPLACE FUNCTION browseai.approve_github_ci_repair(p_tenant uuid,p_task uuid,p_revision bigint,p_failure uuid,
    p_actor uuid,p_approval uuid,p_fence bigint,p_instruction bytea)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE t browseai.tasks%ROWTYPE;
BEGIN
    IF p_revision IS NULL OR p_revision<2 OR p_fence IS NULL OR p_fence<=0 OR p_actor IS NULL OR p_approval IS NULL
       OR p_instruction IS NULL OR octet_length(p_instruction) NOT BETWEEN 1 AND 16384 THEN RETURN false; END IF;
    SELECT * INTO t FROM browseai.tasks WHERE tenant_id=p_tenant AND id=p_task AND revision=p_revision
      AND state='AWAIT_APPROVAL' FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.github_ci_failures f JOIN browseai.github_ci_repair_audit a ON a.failure_id=f.id
    WHERE f.id=p_failure AND f.tenant_id=p_tenant AND f.task_id=p_task AND f.task_revision=p_revision-2
      AND f.repair_at IS NOT NULL AND f.head_sha=t.code_sha AND a.action='repair_reserved'
      AND a.task_revision=p_revision AND a.repairs=t.repairs FOR SHARE OF f,a;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.memberships m JOIN browseai.users u ON u.id=m.user_id
    JOIN browseai.tenants w ON w.id=m.tenant_id
    WHERE m.tenant_id=p_tenant AND m.user_id=p_actor AND m.role='platform_admin'
      AND u.enabled AND w.enabled FOR SHARE OF m,u,w;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.leases l JOIN browseai.projects p ON p.tenant_id=l.tenant_id AND p.id=l.project_id
    JOIN browseai.github_repositories r ON r.tenant_id=p.tenant_id AND r.repository_id=p.repository_id AND lower(r.full_name)=lower(p.repository)
    JOIN browseai.github_installations i ON i.tenant_id=r.tenant_id AND i.installation_id=r.installation_id
    JOIN browseai.github_ci_failures f ON f.id=p_failure AND f.repository_id=r.repository_id AND f.installation_id=i.installation_id
    WHERE l.tenant_id=p_tenant AND l.task_id=p_task AND l.project_id=t.project_id AND l.fence=p_fence
      AND l.expires_at>clock_timestamp() AND l.code_branch=p.code_branch AND l.repository_id=r.repository_id
      AND f.project_id=p.id AND f.code_branch=p.code_branch AND r.enabled AND i.enabled
      AND EXISTS(SELECT 1 FROM browseai.github_binding_audit b WHERE b.tenant_id=r.tenant_id AND b.installation_id=i.installation_id AND b.repository_id=r.repository_id)
      FOR SHARE OF l,p,r,i;
    IF NOT FOUND THEN RETURN false; END IF;
    BEGIN
        INSERT INTO browseai.audit_logs(id,tenant_id,actor_id,action,resource_id,revision)
        VALUES(p_approval,p_tenant,p_actor,'task.ci_repair_approve',p_task,p_revision);
        INSERT INTO browseai.events(tenant_id,audit_id,kind,resource_id,revision)
        VALUES(p_tenant,p_approval,'task.ci_repair_approve',p_task,p_revision);
        INSERT INTO browseai.github_ci_repair_approvals(approval_id,failure_id,tenant_id,task_id,task_revision,stage_index,fence,instruction_digest)
        VALUES(p_approval,p_failure,p_tenant,p_task,p_revision,t.stage_index,p_fence,sha256(p_instruction));
        RETURN true;
    EXCEPTION WHEN unique_violation THEN RETURN false;
    END;
END $$;

CREATE OR REPLACE FUNCTION browseai.decide_github_stage(p_tenant uuid,p_task uuid,p_revision bigint,p_acceptance uuid,
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
      AND EXISTS(SELECT 1 FROM browseai.github_binding_audit b WHERE b.tenant_id=r.tenant_id AND b.installation_id=i.installation_id AND b.repository_id=r.repository_id)
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

CREATE OR REPLACE FUNCTION browseai.confirm_github_ci_repair(p_tenant uuid,p_task uuid,p_revision bigint,p_approval uuid,
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
      AND l.expires_at>clock_timestamp() AND l.code_branch=p.code_branch AND l.repository_id=r.repository_id
      AND f.project_id=p.id AND f.code_branch=p.code_branch AND r.enabled AND i.enabled
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

COMMIT;
