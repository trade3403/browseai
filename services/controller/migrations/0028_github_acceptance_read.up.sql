BEGIN;
-- Only the exact active grant permits reading its two original envelopes.
-- No caller-supplied row ID can widen the result.
CREATE FUNCTION browseai.read_github_acceptance_evidence(p_id uuid,p_tenant uuid,p_task uuid,p_revision bigint,p_actor uuid)
RETURNS TABLE(id uuid,event_type text,statement bytea,signature bytea,evidence_digest bytea,observed_at timestamptz,issued_at timestamptz,expires_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
    SELECT e.id,e.event_type,e.statement,e.signature,e.evidence_digest,e.observed_at,e.issued_at,e.expires_at
    FROM browseai.github_ci_acceptance_grants g
    JOIN browseai.tasks t ON t.tenant_id=g.tenant_id AND t.id=g.task_id AND t.revision=g.task_revision
    JOIN browseai.memberships m ON m.tenant_id=g.tenant_id AND m.user_id=g.actor_id
    JOIN browseai.users u ON u.id=m.user_id
    JOIN browseai.tenants w ON w.id=t.tenant_id
    JOIN browseai.github_ci_evidence e ON e.tenant_id=g.tenant_id AND e.task_id=g.task_id AND e.task_revision=g.task_revision
      AND ((e.id=g.push_id AND e.event_type='push' AND e.evidence_digest=g.push_digest)
        OR (e.id=g.pr_id AND e.event_type='pull_request' AND e.evidence_digest=g.pr_digest))
    WHERE session_user='browseai_ci_acceptor' AND g.id=p_id AND g.tenant_id=p_tenant AND g.task_id=p_task
      AND g.task_revision=p_revision AND g.actor_id=p_actor AND g.revoked_at IS NULL AND g.acceptance_id IS NULL
      AND g.created_at<=statement_timestamp() AND g.expires_at>statement_timestamp()
      AND t.state='WAIT_CI' AND t.code_sha=e.head_sha AND m.role='platform_admin' AND u.enabled AND w.enabled
      AND e.consumed_at IS NULL AND e.expires_at>statement_timestamp()
      AND e.evidence_digest=sha256(e.statement||e.signature)
      AND e.workflow_id=g.workflow_id AND e.source_digest=g.source_digest
    ORDER BY e.event_type
$$;
REVOKE ALL ON FUNCTION browseai.read_github_acceptance_evidence(uuid,uuid,uuid,bigint,uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.read_github_acceptance_evidence(uuid,uuid,uuid,bigint,uuid) TO browseai_ci_acceptor;
COMMIT;
