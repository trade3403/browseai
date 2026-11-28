BEGIN;
GRANT USAGE ON SCHEMA browseai TO browseai_failure_observer;
-- Provisioned by the trusted migration/operator boundary, not by the observer.
-- One exact task revision and pinned policy per grant; disabled/expired grants
-- cannot record. No retroactive approvals or general owner credentials.
CREATE TABLE browseai.github_failure_observer_grants (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id uuid NOT NULL,
    task_id uuid NOT NULL,
    project_id uuid NOT NULL,
    task_revision bigint NOT NULL CHECK(task_revision>=0),
    installation_id bigint NOT NULL CHECK(installation_id>0),
    repository_id bigint NOT NULL CHECK(repository_id>0),
    code_branch text NOT NULL CHECK(length(code_branch) BETWEEN 1 AND 255),
    head_sha text NOT NULL CHECK(head_sha ~ '^[0-9a-f]{40}$'),
    workflow_id bigint NOT NULL CHECK(workflow_id>0),
    source_digest text NOT NULL CHECK(source_digest ~ '^[0-9a-f]{64}$'),
    event_type text NOT NULL CHECK(event_type IN ('push','pull_request')),
    approved_by text NOT NULL DEFAULT session_user,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    failure_id uuid UNIQUE REFERENCES browseai.github_ci_failures(id),
    FOREIGN KEY(tenant_id,task_id,project_id) REFERENCES browseai.tasks(tenant_id,id,project_id),
    UNIQUE(tenant_id,task_id,task_revision,workflow_id,event_type),
    CHECK(isfinite(created_at) AND isfinite(expires_at) AND expires_at>created_at AND expires_at<=created_at+interval '24 hours'),
    CHECK(revoked_at IS NULL OR (isfinite(revoked_at) AND revoked_at>=created_at))
);
REVOKE ALL ON browseai.github_failure_observer_grants FROM PUBLIC;
ALTER TABLE browseai.github_failure_observer_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.github_failure_observer_grants FORCE ROW LEVEL SECURITY;

-- The observer remains trusted to make independent GitHub reads. SQL
-- limits the blast radius of that identity; it does not verify GitHub I/O.
CREATE FUNCTION browseai.record_authorized_github_failure(p_grant uuid,p_tenant uuid,p_task uuid,p_revision bigint,
    p_project uuid,p_installation bigint,p_repository bigint,p_branch text,p_sha text,p_workflow bigint,p_source text,
    p_event text,p_run bigint,p_attempt bigint,p_jobs bigint[],p_observed timestamptz)
RETURNS uuid LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE g browseai.github_failure_observer_grants%ROWTYPE; result uuid;
BEGIN
    IF session_user<>'browseai_failure_observer' OR p_grant IS NULL THEN RETURN NULL; END IF;
    -- Task first, then grant, matching other task transition lock order.
    PERFORM 1 FROM browseai.tasks WHERE tenant_id=p_tenant AND id=p_task AND project_id=p_project
      AND revision=p_revision AND state='WAIT_CI' AND code_sha=p_sha FOR UPDATE;
    IF NOT FOUND THEN RETURN NULL; END IF;
    SELECT * INTO g FROM browseai.github_failure_observer_grants WHERE id=p_grant AND tenant_id=p_tenant
      AND task_id=p_task AND project_id=p_project AND task_revision=p_revision AND installation_id=p_installation
      AND repository_id=p_repository AND code_branch=p_branch AND head_sha=p_sha AND workflow_id=p_workflow
      AND source_digest=p_source AND event_type=p_event AND revoked_at IS NULL AND failure_id IS NULL
      AND created_at<=clock_timestamp() AND expires_at>clock_timestamp() FOR UPDATE;
    IF NOT FOUND THEN RETURN NULL; END IF;
    result:=browseai.record_github_ci_failure(p_tenant,p_task,p_revision,p_project,p_installation,p_repository,
      p_branch,p_sha,p_workflow,p_source,p_event,p_run,p_attempt,p_jobs,p_observed);
    IF result IS NULL THEN RETURN NULL; END IF;
    -- Roll back the complete state transition if the grant expired while
    -- waiting for a binding lock. The caller must discard the error.
    IF g.expires_at<=clock_timestamp() THEN RAISE EXCEPTION 'observer grant expired'; END IF;
    UPDATE browseai.github_failure_observer_grants SET failure_id=result WHERE id=g.id AND failure_id IS NULL;
    IF NOT FOUND THEN RAISE EXCEPTION 'observer grant consumption lost'; END IF;
    RETURN result;
END $$;
REVOKE ALL ON FUNCTION browseai.record_authorized_github_failure(uuid,uuid,uuid,bigint,uuid,bigint,bigint,text,text,bigint,text,text,bigint,bigint,bigint[],timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.record_authorized_github_failure(uuid,uuid,uuid,bigint,uuid,bigint,bigint,text,text,bigint,text,text,bigint,bigint,bigint[],timestamptz) TO browseai_failure_observer;
COMMIT;
