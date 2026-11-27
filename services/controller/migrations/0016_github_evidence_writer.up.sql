BEGIN;
GRANT USAGE ON SCHEMA browseai TO browseai_evidence_writer;
-- This role is reserved for the isolated trusted Go ingester. PostgreSQL
-- does not verify Ed25519; the caller MUST have verified canonical v2 bytes.
-- Derive all signed columns from those bytes, not separate caller metadata.
CREATE FUNCTION browseai.insert_github_ci_evidence(p_statement bytea,p_signature bytea,p_observed timestamptz)
RETURNS uuid LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE s jsonb; e jsonb; result uuid; issued timestamptz; key_until timestamptz; expiry timestamptz;
BEGIN
    IF p_statement IS NULL OR octet_length(p_statement) NOT BETWEEN 1 AND 131072
       OR p_signature IS NULL OR octet_length(p_signature)<>64
       OR p_observed IS NULL OR NOT isfinite(p_observed) THEN
        RAISE EXCEPTION 'invalid evidence' USING ERRCODE='22023';
    END IF;
    s:=convert_from(p_statement,'UTF8')::jsonb;
    e:=s->'execution';
    IF s->>'domain' IS DISTINCT FROM 'browseai-ci-task-execution-v2'
       OR e->>'domain' IS DISTINCT FROM 'browseai-ci-execution-v1'
       OR e->>'event' IS NULL OR e->>'event' NOT IN ('push','pull_request')
       OR (e->>'event'='push' AND (e->>'executed_sha' IS DISTINCT FROM e->>'head_sha'
           OR e->>'base_sha' IS DISTINCT FROM '' OR e->>'base_branch' IS DISTINCT FROM '' OR e->>'pr' IS DISTINCT FROM '0')) THEN
        RAISE EXCEPTION 'invalid evidence' USING ERRCODE='22023';
    END IF;
    issued:=to_timestamp((e->>'issued_unix')::bigint);
    SELECT v.valid_until INTO key_until FROM browseai.github_verifier_keys v
    JOIN browseai.github_repositories r ON r.tenant_id=v.tenant_id AND r.repository_id=v.repository_id AND r.installation_id=v.installation_id
    WHERE v.key_id=(s->>'key')::uuid AND v.verifier_id=(s->>'verifier')::uuid
      AND v.tenant_id=(e->>'tenant')::uuid AND v.repository_id=(e->>'repository')::bigint
      AND v.installation_id=(e->>'installation')::bigint AND r.full_name=e->>'full_name'
      AND v.revoked_at IS NULL AND r.enabled
    FOR SHARE OF v,r;
    IF NOT FOUND THEN RAISE EXCEPTION 'evidence scope unavailable'; END IF;
    expiry:=least(p_observed+interval '30 seconds',issued+interval '24 hours',key_until);
    INSERT INTO browseai.github_ci_evidence(tenant_id,project_id,task_id,task_revision,installation_id,repository_id,code_branch,
        head_sha,base_branch,base_sha,merge_sha,pr_number,event_type,workflow_id,workflow_name,workflow_path,source_digest,
        run_id,attempt,job_ids,required_jobs,verifier_id,key_id,statement,signature,evidence_digest,observed_at,issued_at,expires_at)
    VALUES((e->>'tenant')::uuid,(s->>'project')::uuid,(s->>'task')::uuid,(s->>'revision')::bigint,
        (e->>'installation')::bigint,(e->>'repository')::bigint,e->>'branch',e->>'head_sha',
        CASE WHEN e->>'event'='pull_request' THEN e->>'base_branch' END,
        CASE WHEN e->>'event'='pull_request' THEN e->>'base_sha' END,
        CASE WHEN e->>'event'='pull_request' THEN e->>'executed_sha' END,
        CASE WHEN e->>'event'='pull_request' THEN (e->>'pr')::bigint END,
        e->>'event',(e->>'workflow')::bigint,e->>'name',e->>'path',e->>'source_digest',
        (e->>'run')::bigint,(e->>'attempt')::bigint,
        ARRAY(SELECT x::bigint FROM jsonb_array_elements_text(e->'jobs') AS j(x)),
        ARRAY(SELECT x FROM jsonb_array_elements_text(e->'required') AS j(x)),
        (s->>'verifier')::uuid,(s->>'key')::uuid,p_statement,p_signature,sha256(p_statement||p_signature),p_observed,issued,expiry)
    RETURNING id INTO result;
    RETURN result;
END $$;
REVOKE ALL ON FUNCTION browseai.insert_github_ci_evidence(bytea,bytea,timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.insert_github_ci_evidence(bytea,bytea,timestamptz) TO browseai_evidence_writer;
COMMIT;
