BEGIN;
ALTER TABLE browseai.github_ci_evidence ADD COLUMN consumed_at timestamptz;
ALTER TABLE browseai.github_ci_evidence ADD CONSTRAINT github_ci_evidence_consumption_time
    CHECK (consumed_at IS NULL OR (isfinite(consumed_at) AND consumed_at>=created_at AND consumed_at<expires_at));

-- Internal transaction primitive, deliberately not granted to any runtime
-- role. The future state machine must validate the trusted policy, recheck
-- remote execution, consume all required evidence and advance in ONE transaction.
CREATE FUNCTION browseai.consume_github_ci_evidence(p_tenant uuid,p_task uuid,p_revision bigint,p_id uuid,p_digest bytea)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE candidate browseai.github_ci_evidence%ROWTYPE; n timestamptz;
BEGIN
    IF p_tenant IS NULL OR p_task IS NULL OR p_revision IS NULL OR p_revision<0 OR p_id IS NULL
       OR p_digest IS NULL OR octet_length(p_digest)<>32 THEN RETURN false; END IF;
    -- Serialize consumers of this task, including different evidence rows.
    PERFORM 1 FROM browseai.tasks t WHERE t.tenant_id=p_tenant AND t.id=p_task
      AND t.revision=p_revision AND t.state='WAIT_CI' FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    SELECT c.* INTO candidate FROM browseai.github_ci_evidence c
    WHERE c.tenant_id=p_tenant AND c.task_id=p_task AND c.task_revision=p_revision
      AND c.id=p_id AND c.evidence_digest=p_digest AND c.consumed_at IS NULL
    FOR UPDATE;
    IF NOT FOUND THEN RETURN false; END IF;
    -- Recheck live authorization under row locks. A successful revocation
    -- before this check invalidates even a previously persisted signature.
    PERFORM 1 FROM browseai.tasks t
    JOIN browseai.projects p ON p.tenant_id=t.tenant_id AND p.id=t.project_id
    JOIN browseai.tenants w ON w.id=t.tenant_id
    JOIN browseai.github_repositories r ON r.tenant_id=p.tenant_id AND r.repository_id=p.repository_id AND lower(r.full_name)=lower(p.repository)
    JOIN browseai.github_installations i ON i.tenant_id=r.tenant_id AND i.installation_id=r.installation_id
    JOIN browseai.github_verifier_keys v ON v.tenant_id=r.tenant_id AND v.installation_id=i.installation_id AND v.repository_id=r.repository_id
    WHERE t.tenant_id=p_tenant AND t.id=p_task AND t.revision=p_revision AND t.state='WAIT_CI'
      AND t.project_id=candidate.project_id AND t.code_sha=candidate.head_sha
      AND p.code_branch=candidate.code_branch AND r.repository_id=candidate.repository_id
      AND i.installation_id=candidate.installation_id AND w.enabled AND r.enabled AND i.enabled
      AND v.key_id=candidate.key_id AND v.verifier_id=candidate.verifier_id AND v.revoked_at IS NULL
      AND v.valid_from<=candidate.issued_at AND v.valid_until>=candidate.expires_at
      AND EXISTS(SELECT 1 FROM browseai.github_binding_audit a WHERE a.tenant_id=r.tenant_id AND a.installation_id=i.installation_id AND a.repository_id=r.repository_id)
    FOR SHARE OF p,w,r,i,v;
    IF NOT FOUND THEN RETURN false; END IF;
    n:=clock_timestamp();
    IF n<candidate.created_at OR n<candidate.observed_at OR n<candidate.issued_at OR n>=candidate.expires_at
       OR n>=candidate.observed_at+interval '30 seconds' OR n>=candidate.issued_at+interval '24 hours'
       OR candidate.evidence_digest<>sha256(candidate.statement||candidate.signature) THEN RETURN false; END IF;
    UPDATE browseai.github_ci_evidence SET consumed_at=n
    WHERE id=p_id AND tenant_id=p_tenant AND task_id=p_task AND task_revision=p_revision AND consumed_at IS NULL;
    RETURN FOUND;
END $$;
REVOKE ALL ON FUNCTION browseai.consume_github_ci_evidence(uuid,uuid,bigint,uuid,bytea) FROM PUBLIC;
COMMIT;
