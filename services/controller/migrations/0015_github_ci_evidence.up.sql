BEGIN;
-- Sealed ledger foundation. No runtime role may insert, read or consume
-- evidence until the trusted Go ingestion and task-bound proof are wired.
CREATE TABLE browseai.github_ci_evidence (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL,
    project_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_revision bigint NOT NULL CHECK (task_revision>=0),
    installation_id bigint NOT NULL,
    repository_id bigint NOT NULL,
    code_branch text NOT NULL,
    head_sha text NOT NULL CHECK (head_sha ~ '^[0-9a-f]{40}$'),
    base_branch text,
    base_sha text CHECK (base_sha ~ '^[0-9a-f]{40}$'),
    merge_sha text CHECK (merge_sha ~ '^[0-9a-f]{40}$'),
    pr_number bigint CHECK (pr_number>0),
    event_type text NOT NULL CHECK (event_type IN ('push','pull_request')),
    workflow_id bigint NOT NULL CHECK (workflow_id>0),
    workflow_name text NOT NULL CHECK (length(workflow_name) BETWEEN 1 AND 255),
    workflow_path text NOT NULL CHECK (workflow_path ~ '^\.github/workflows/[^/]+\.ya?ml$'),
    source_digest text NOT NULL CHECK (source_digest ~ '^[0-9a-f]{64}$'),
    run_id bigint NOT NULL CHECK (run_id>0),
    attempt bigint NOT NULL CHECK (attempt>0),
    job_ids bigint[] NOT NULL CHECK (array_ndims(job_ids)=1 AND cardinality(job_ids) BETWEEN 1 AND 1000 AND array_position(job_ids,NULL) IS NULL AND 0<ALL(job_ids)),
    required_jobs text[] NOT NULL CHECK (array_ndims(required_jobs)=1 AND cardinality(required_jobs) BETWEEN 1 AND 100 AND array_position(required_jobs,NULL) IS NULL AND ''<>ALL(required_jobs)),
    verifier_id uuid NOT NULL,
    key_id uuid NOT NULL REFERENCES browseai.github_verifier_keys(key_id),
    statement bytea NOT NULL CHECK (octet_length(statement) BETWEEN 1 AND 131072),
    signature bytea NOT NULL UNIQUE CHECK (octet_length(signature)=64),
    evidence_digest bytea NOT NULL UNIQUE CHECK (octet_length(evidence_digest)=32 AND evidence_digest=sha256(statement||signature)),
    observed_at timestamptz NOT NULL,
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE(tenant_id,repository_id,workflow_id,event_type,run_id,attempt),
    FOREIGN KEY(tenant_id,task_id,project_id) REFERENCES browseai.tasks(tenant_id,id,project_id),
    FOREIGN KEY(tenant_id,project_id,repository_id,code_branch) REFERENCES browseai.projects(tenant_id,id,repository_id,code_branch),
    FOREIGN KEY(tenant_id,installation_id) REFERENCES browseai.github_installations(tenant_id,installation_id),
    FOREIGN KEY(tenant_id,repository_id) REFERENCES browseai.github_repositories(tenant_id,repository_id),
    CHECK ((event_type='push' AND base_branch IS NULL AND base_sha IS NULL AND merge_sha IS NULL AND pr_number IS NULL)
        OR (event_type='pull_request' AND base_branch IS NOT NULL AND length(base_branch) BETWEEN 1 AND 255
            AND base_sha IS NOT NULL AND merge_sha IS NOT NULL AND pr_number IS NOT NULL
            AND head_sha<>base_sha AND merge_sha<>head_sha AND merge_sha<>base_sha)),
    CHECK (isfinite(observed_at) AND isfinite(issued_at) AND isfinite(expires_at)
        AND expires_at>observed_at AND expires_at>issued_at
        AND expires_at<=observed_at+interval '30 seconds' AND expires_at<=issued_at+interval '24 hours')
);
CREATE INDEX github_ci_evidence_task_idx ON browseai.github_ci_evidence(tenant_id,task_id,task_revision);
ALTER TABLE browseai.github_ci_evidence ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.github_ci_evidence FORCE ROW LEVEL SECURITY;
REVOKE ALL ON browseai.github_ci_evidence FROM PUBLIC;

-- Defense in depth for the future trusted ingester, not a signature verifier.
-- Recheck live scope and lock the authorization/task rows through insertion.
CREATE FUNCTION browseai.guard_github_ci_evidence()
RETURNS trigger LANGUAGE plpgsql VOLATILE SET search_path=pg_catalog,pg_temp AS $$
DECLARE key_until timestamptz; n timestamptz;
BEGIN
    SELECT v.valid_until INTO key_until
    FROM browseai.tasks t
    JOIN browseai.projects p ON p.tenant_id=t.tenant_id AND p.id=t.project_id
    JOIN browseai.tenants w ON w.id=t.tenant_id
    JOIN browseai.github_repositories r ON r.tenant_id=p.tenant_id AND r.repository_id=p.repository_id AND lower(r.full_name)=lower(p.repository)
    JOIN browseai.github_installations i ON i.tenant_id=r.tenant_id AND i.installation_id=r.installation_id
    JOIN browseai.github_verifier_keys v ON v.tenant_id=r.tenant_id AND v.installation_id=i.installation_id AND v.repository_id=r.repository_id
    WHERE t.tenant_id=NEW.tenant_id AND t.id=NEW.task_id AND t.project_id=NEW.project_id
      AND t.revision=NEW.task_revision AND t.state='WAIT_CI' AND t.code_sha=NEW.head_sha
      AND p.code_branch=NEW.code_branch AND r.repository_id=NEW.repository_id AND i.installation_id=NEW.installation_id
      AND v.key_id=NEW.key_id AND v.verifier_id=NEW.verifier_id AND v.revoked_at IS NULL
      AND w.enabled AND r.enabled AND i.enabled AND v.valid_from<=NEW.issued_at
      AND v.valid_until>NEW.issued_at
      AND EXISTS(SELECT 1 FROM browseai.github_binding_audit a WHERE a.tenant_id=r.tenant_id AND a.installation_id=i.installation_id AND a.repository_id=r.repository_id)
    FOR SHARE OF t,p,w,r,i,v;
    n:=clock_timestamp();
    IF NOT FOUND OR NEW.observed_at>n OR NEW.issued_at>n OR NEW.expires_at<=n OR NEW.expires_at>key_until
       OR NEW.observed_at<=n-interval '30 seconds' OR NEW.issued_at<=n-interval '24 hours'
       OR cardinality(NEW.job_ids)<>(SELECT count(DISTINCT x) FROM unnest(NEW.job_ids) AS u(x))
       OR cardinality(NEW.required_jobs)<>(SELECT count(DISTINCT x) FROM unnest(NEW.required_jobs) AS u(x)) THEN
        RAISE EXCEPTION 'evidence scope or freshness unavailable';
    END IF;
    NEW.created_at:=n;
    RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION browseai.guard_github_ci_evidence() FROM PUBLIC;
CREATE TRIGGER github_ci_evidence_guard BEFORE INSERT ON browseai.github_ci_evidence
FOR EACH ROW EXECUTE FUNCTION browseai.guard_github_ci_evidence();
COMMIT;
