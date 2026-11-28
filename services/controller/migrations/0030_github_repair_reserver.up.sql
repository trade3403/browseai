BEGIN;
GRANT USAGE ON SCHEMA browseai TO browseai_repair_reserver;

-- A reservation is not approval or dispatch. Only a recorded failure from
-- a consumed independent observation grant can enter this runtime path.
CREATE FUNCTION browseai.reserve_authorized_github_repair(p_grant uuid,p_tenant uuid,p_task uuid,p_revision bigint,p_failure uuid,p_fence bigint)
RETURNS text LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE t browseai.tasks%ROWTYPE; lease_until timestamptz; target text;
BEGIN
    IF session_user<>'browseai_repair_reserver' OR p_grant IS NULL OR p_failure IS NULL OR p_revision IS NULL
       OR p_revision<=0 OR p_revision=9223372036854775807 OR p_fence IS NULL OR p_fence<=0 THEN RETURN NULL; END IF;
    SELECT * INTO t FROM browseai.tasks WHERE tenant_id=p_tenant AND id=p_task AND revision=p_revision AND state='CI_FAILED' FOR UPDATE;
    IF NOT FOUND THEN RETURN NULL; END IF;
    PERFORM 1 FROM browseai.github_failure_observer_grants g
    JOIN browseai.github_ci_failures f ON f.id=g.failure_id
    WHERE g.id=p_grant AND g.tenant_id=p_tenant AND g.task_id=p_task AND g.task_revision=p_revision-1
      AND g.revoked_at IS NULL AND g.failure_id=p_failure
      AND f.tenant_id=g.tenant_id AND f.task_id=g.task_id AND f.task_revision=g.task_revision
      AND f.project_id=g.project_id AND f.project_id=t.project_id AND f.code_branch=g.code_branch
      AND f.repository_id=g.repository_id AND f.installation_id=g.installation_id
      AND f.head_sha=g.head_sha AND f.head_sha=t.code_sha AND f.workflow_id=g.workflow_id
      AND f.source_digest=g.source_digest AND f.event_type=g.event_type AND f.repair_at IS NULL
      AND f.created_at>=g.created_at AND f.created_at<g.expires_at
    FOR SHARE OF g,f;
    IF NOT FOUND THEN RETURN NULL; END IF;
    -- Lock the exact stage plan for this task before inspecting it.
    PERFORM 1 FROM browseai.stages WHERE tenant_id=p_tenant AND task_id=p_task FOR SHARE;
    IF (SELECT count(*) FROM browseai.stages WHERE tenant_id=p_tenant AND task_id=p_task)<>t.stage_count
       OR EXISTS(SELECT 1 FROM browseai.stages s WHERE s.tenant_id=p_tenant AND s.task_id=p_task AND
       (s.ordinal>=t.stage_count OR s.stage_id !~ '^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$'
        OR s.instruction IS NULL OR s.instruction !~ '[^[:space:]]'
        OR cardinality(s.acceptance) NOT BETWEEN 1 AND 50
        OR array_ndims(s.acceptance)<>1 OR coalesce(array_ndims(s.dependencies),1)<>1
        OR EXISTS(SELECT 1 FROM unnest(s.acceptance) AS x(v) WHERE v IS NULL OR octet_length(v) NOT BETWEEN 1 AND 128
              OR v !~ '[^[:space:]]' OR v ~ '^[[:space:]]|[[:space:]]$')
        OR cardinality(s.acceptance)<>(SELECT count(DISTINCT v) FROM unnest(s.acceptance) AS x(v))
        OR cardinality(s.dependencies)<>(SELECT count(DISTINCT v) FROM unnest(s.dependencies) AS x(v))
        OR EXISTS(SELECT 1 FROM unnest(s.dependencies) AS x(v) WHERE v IS NULL OR NOT EXISTS
          (SELECT 1 FROM browseai.stages d WHERE d.tenant_id=s.tenant_id AND d.task_id=s.task_id
            AND d.stage_id=v AND d.ordinal<s.ordinal)))) THEN RETURN NULL; END IF;
    SELECT l.expires_at INTO lease_until FROM browseai.leases l WHERE l.tenant_id=p_tenant AND l.task_id=p_task
      AND l.project_id=t.project_id AND l.fence=p_fence AND l.expires_at>clock_timestamp() FOR SHARE;
    IF NOT FOUND THEN RETURN NULL; END IF;
    BEGIN
        target:=browseai.reserve_github_ci_repair(p_tenant,p_task,p_revision,p_failure,p_fence);
        IF target IS NULL OR clock_timestamp()>=lease_until THEN RAISE EXCEPTION 'repair reservation unavailable' USING ERRCODE='PBA03'; END IF;
        RETURN target;
    EXCEPTION WHEN SQLSTATE 'PBA03' THEN RETURN NULL;
    END;
END $$;
REVOKE ALL ON FUNCTION browseai.reserve_authorized_github_repair(uuid,uuid,uuid,bigint,uuid,bigint) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.reserve_authorized_github_repair(uuid,uuid,uuid,bigint,uuid,bigint) TO browseai_repair_reserver;
COMMIT;
