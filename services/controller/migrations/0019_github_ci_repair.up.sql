BEGIN;
-- Sealed trusted-observer boundary. No runtime grants.
CREATE TABLE browseai.github_ci_failures (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_revision bigint NOT NULL CHECK (task_revision>=0),
    repository_id bigint NOT NULL,
    installation_id bigint NOT NULL,
    workflow_id bigint NOT NULL CHECK (workflow_id>0),
    event_type text NOT NULL CHECK (event_type IN ('push','pull_request')),
    run_id bigint NOT NULL CHECK (run_id>0),
    attempt bigint NOT NULL CHECK (attempt>0),
    head_sha text NOT NULL CHECK (head_sha ~ '^[0-9a-f]{40}$'),
    source_digest text NOT NULL CHECK (source_digest ~ '^[0-9a-f]{64}$'),
    failed_jobs bigint[] NOT NULL CHECK (cardinality(failed_jobs) BETWEEN 1 AND 100 AND array_ndims(failed_jobs)=1 AND array_position(failed_jobs,NULL) IS NULL AND 0<ALL(failed_jobs)),
    observed_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    repair_at timestamptz,
    FOREIGN KEY (tenant_id,task_id) REFERENCES browseai.tasks(tenant_id,id),
    UNIQUE (tenant_id,task_id,task_revision),
    UNIQUE (tenant_id,repository_id,workflow_id,event_type,run_id,attempt),
    CHECK (isfinite(observed_at) AND isfinite(created_at) AND created_at>=observed_at AND created_at<observed_at+interval '30 seconds'),
    CHECK (repair_at IS NULL OR (isfinite(repair_at) AND repair_at>=created_at))
);
REVOKE ALL ON browseai.github_ci_failures FROM PUBLIC;
ALTER TABLE browseai.github_ci_failures ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.github_ci_failures FORCE ROW LEVEL SECURITY;

CREATE FUNCTION browseai.record_github_ci_failure(p_tenant uuid,p_task uuid,p_revision bigint,p_project uuid,
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
    INSERT INTO browseai.github_ci_failures(tenant_id,task_id,task_revision,repository_id,installation_id,workflow_id,event_type,run_id,attempt,head_sha,source_digest,failed_jobs,observed_at,created_at)
    VALUES(p_tenant,p_task,p_revision,p_repository,p_installation,p_workflow,p_event,p_run,p_attempt,p_sha,p_source,p_jobs,p_observed,n)
    ON CONFLICT DO NOTHING RETURNING id INTO result;
    IF result IS NULL THEN RETURN NULL; END IF;
    UPDATE browseai.tasks SET state='CI_FAILED',revision=revision+1,updated_at=n
    WHERE tenant_id=p_tenant AND id=p_task AND revision=p_revision AND state='WAIT_CI';
    IF NOT FOUND THEN RAISE EXCEPTION 'failure transition lost'; END IF;
    RETURN result;
END $$;

-- Reserve at most one repair against a recorded failure. An exhausted
-- budget is escalated instead of reserving another send. The trusted caller
-- must validate the stage plan and reconcile the branch before calling.
CREATE FUNCTION browseai.reserve_github_ci_repair(p_tenant uuid,p_task uuid,p_revision bigint,p_failure uuid,p_fence bigint)
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
      AND l.expires_at>clock_timestamp() AND r.repository_id=failure_row.repository_id AND i.installation_id=failure_row.installation_id
      AND t.enabled AND r.enabled AND i.enabled FOR SHARE OF p,t,r,i,l;
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
REVOKE ALL ON FUNCTION browseai.record_github_ci_failure(uuid,uuid,bigint,uuid,bigint,bigint,text,text,bigint,text,text,bigint,bigint,bigint[],timestamptz),
    browseai.reserve_github_ci_repair(uuid,uuid,bigint,uuid,bigint) FROM PUBLIC;
COMMIT;
