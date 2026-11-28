BEGIN;
GRANT USAGE ON SCHEMA browseai TO browseai_stage_human;
CREATE FUNCTION browseai.review_authorized_github_stage(p_digest bytea,p_tenant uuid,p_task uuid,p_revision bigint,p_acceptance uuid,p_decision uuid,p_action text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE t browseai.tasks%ROWTYPE; actor uuid; session_until timestamptz; accepted boolean;
BEGIN
    IF session_user<>'browseai_stage_human' OR p_digest IS NULL OR octet_length(p_digest)<>32
       OR p_revision IS NULL OR p_revision<2 OR p_revision=9223372036854775807
       OR p_acceptance IS NULL OR p_decision IS NULL OR p_acceptance=p_decision
       OR p_action IS NULL OR p_action NOT IN ('next_stage','complete') THEN RETURN false; END IF;
    SELECT * INTO t FROM browseai.tasks WHERE tenant_id=p_tenant AND id=p_task AND revision=p_revision AND state='STAGE_PASSED' FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    SELECT s.user_id INTO actor FROM browseai.auth_sessions s WHERE s.token_digest=p_digest AND s.tenant_id=p_tenant;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.users WHERE id=actor AND enabled FOR SHARE;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.tenants WHERE id=p_tenant AND enabled FOR SHARE;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM 1 FROM browseai.memberships WHERE user_id=actor AND tenant_id=p_tenant AND role='platform_admin' FOR SHARE;
    IF NOT FOUND THEN RETURN false; END IF;
    SELECT s.expires_at INTO session_until FROM browseai.auth_sessions s WHERE s.token_digest=p_digest
      AND s.tenant_id=p_tenant AND s.user_id=actor AND s.revoked_at IS NULL AND s.expires_at>clock_timestamp() FOR SHARE;
    IF NOT FOUND THEN RETURN false; END IF;
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
            AND d.stage_id=v AND d.ordinal<s.ordinal)))) THEN RETURN false; END IF;
    BEGIN
        accepted:=browseai.decide_github_stage(p_tenant,p_task,p_revision,p_acceptance,actor,p_decision,p_action);
        IF NOT accepted OR clock_timestamp()>=session_until THEN
            RAISE EXCEPTION 'stage review unavailable' USING ERRCODE='PBA04';
        END IF;
        RETURN true;
    EXCEPTION WHEN SQLSTATE 'PBA04' THEN RETURN false;
    END;
END $$;
REVOKE ALL ON FUNCTION browseai.review_authorized_github_stage(bytea,uuid,uuid,bigint,uuid,uuid,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.review_authorized_github_stage(bytea,uuid,uuid,bigint,uuid,uuid,text) TO browseai_stage_human;
COMMIT;
