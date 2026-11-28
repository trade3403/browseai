BEGIN;
GRANT USAGE ON SCHEMA browseai TO browseai_repair_human;

-- Only the trusted application endpoint may submit a session digest.
-- The actor is resolved inside the mutation, never supplied by the caller.
CREATE FUNCTION browseai.review_authorized_github_repair(p_digest bytea,p_tenant uuid,p_task uuid,p_revision bigint,
    p_grant uuid,p_failure uuid,p_approval uuid,p_confirmation uuid,p_fence bigint,p_instruction bytea,p_action text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE t browseai.tasks%ROWTYPE; actor uuid; session_until timestamptz; lease_until timestamptz; accepted boolean;
BEGIN
    IF session_user<>'browseai_repair_human' OR p_digest IS NULL OR octet_length(p_digest)<>32
       OR p_revision IS NULL OR p_revision<2 OR p_revision=9223372036854775807 OR p_fence IS NULL OR p_fence<=0
       OR p_grant IS NULL OR p_failure IS NULL OR p_approval IS NULL OR p_instruction IS NULL
       OR octet_length(p_instruction) NOT BETWEEN 1 AND 16384 OR p_action IS NULL
       OR p_action NOT IN ('approve','confirm_sent') OR (p_action='approve' AND p_confirmation IS NOT NULL)
       OR (p_action='confirm_sent' AND (p_confirmation IS NULL OR p_confirmation=p_approval)) THEN RETURN false; END IF;
    IF convert_from(p_instruction,'UTF8') !~ '[^[:space:]]' THEN RETURN false; END IF;
    SELECT * INTO t FROM browseai.tasks WHERE tenant_id=p_tenant AND id=p_task AND revision=p_revision
      AND state='AWAIT_APPROVAL' AND repairs>0 FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    -- Lock identities before the session, consistent with account disable/reset.
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
    PERFORM 1 FROM browseai.github_failure_observer_grants g JOIN browseai.github_ci_failures f ON f.id=g.failure_id
    JOIN browseai.github_ci_repair_audit a ON a.failure_id=f.id
    WHERE g.id=p_grant AND g.tenant_id=p_tenant AND g.task_id=p_task AND g.task_revision=p_revision-2
      AND g.revoked_at IS NULL AND f.id=p_failure AND f.tenant_id=g.tenant_id AND f.task_id=g.task_id
      AND f.task_revision=g.task_revision AND f.project_id=g.project_id AND f.project_id=t.project_id
      AND f.code_branch=g.code_branch AND f.repository_id=g.repository_id AND f.installation_id=g.installation_id
      AND f.head_sha=g.head_sha AND f.head_sha=t.code_sha AND f.workflow_id=g.workflow_id
      AND f.source_digest=g.source_digest AND f.event_type=g.event_type
      AND f.created_at>=g.created_at AND f.created_at<g.expires_at AND f.repair_at IS NOT NULL
      AND a.action='repair_reserved' AND a.task_revision=p_revision AND a.repairs=t.repairs
      AND a.observer_role='browseai_repair_reserver' FOR SHARE OF g,f,a;
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
    SELECT l.expires_at INTO lease_until FROM browseai.leases l WHERE l.tenant_id=p_tenant AND l.task_id=p_task
      AND l.project_id=t.project_id AND l.fence=p_fence AND l.expires_at>clock_timestamp() FOR SHARE;
    IF NOT FOUND THEN RETURN false; END IF;
    IF p_action='confirm_sent' AND NOT EXISTS(SELECT 1 FROM browseai.github_ci_repair_approvals a
       WHERE a.approval_id=p_approval AND a.failure_id=p_failure AND a.tenant_id=p_tenant AND a.task_id=p_task
       AND a.task_revision=p_revision) THEN RETURN false; END IF;
    BEGIN
        IF p_action='approve' THEN
            accepted:=browseai.approve_github_ci_repair(p_tenant,p_task,p_revision,p_failure,actor,p_approval,p_fence,p_instruction);
        ELSE
            accepted:=browseai.confirm_github_ci_repair(p_tenant,p_task,p_revision,p_approval,actor,p_confirmation,p_fence,p_instruction);
        END IF;
        IF NOT accepted OR clock_timestamp()>=least(session_until,lease_until) THEN
            RAISE EXCEPTION 'repair review unavailable' USING ERRCODE='PBA04';
        END IF;
        RETURN true;
    EXCEPTION WHEN SQLSTATE 'PBA04' THEN RETURN false;
    END;
EXCEPTION WHEN character_not_in_repertoire OR untranslatable_character THEN RETURN false;
END $$;
REVOKE ALL ON FUNCTION browseai.review_authorized_github_repair(bytea,uuid,uuid,bigint,uuid,uuid,uuid,uuid,bigint,bytea,text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.review_authorized_github_repair(bytea,uuid,uuid,bigint,uuid,uuid,uuid,uuid,bigint,bytea,text) TO browseai_repair_human;
COMMIT;
