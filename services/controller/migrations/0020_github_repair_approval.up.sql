BEGIN;
-- System observations have a database identity, never an invented human ID.
-- No runtime privileges are granted on the sealed history or approval entry.
CREATE TABLE browseai.github_ci_repair_audit (
    failure_id uuid NOT NULL REFERENCES browseai.github_ci_failures(id),
    action text NOT NULL CHECK (action IN ('failure_recorded','repair_reserved','repair_exhausted')),
    task_revision bigint NOT NULL CHECK (task_revision>=0),
    target_state text NOT NULL,
    repairs integer NOT NULL CHECK (repairs BETWEEN 0 AND 20),
    observer_role text NOT NULL DEFAULT session_user,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(failure_id,action),
    CHECK ((action='failure_recorded' AND target_state='CI_FAILED')
       OR (action='repair_reserved' AND target_state='AWAIT_APPROVAL')
       OR (action='repair_exhausted' AND target_state='NEED_HUMAN'))
);
CREATE TABLE browseai.github_ci_repair_approvals (
    approval_id uuid PRIMARY KEY REFERENCES browseai.audit_logs(id),
    failure_id uuid NOT NULL UNIQUE REFERENCES browseai.github_ci_failures(id),
    tenant_id uuid NOT NULL,
    task_id uuid NOT NULL,
    task_revision bigint NOT NULL CHECK (task_revision>=0),
    stage_index integer NOT NULL CHECK (stage_index BETWEEN 0 AND 99),
    fence bigint NOT NULL CHECK (fence>0),
    instruction_digest bytea NOT NULL CHECK (octet_length(instruction_digest)=32),
    UNIQUE(tenant_id,task_id,task_revision),
    FOREIGN KEY(tenant_id,task_id) REFERENCES browseai.tasks(tenant_id,id)
);
REVOKE ALL ON browseai.github_ci_repair_audit,browseai.github_ci_repair_approvals FROM PUBLIC;
ALTER TABLE browseai.github_ci_repair_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.github_ci_repair_audit FORCE ROW LEVEL SECURITY;
ALTER TABLE browseai.github_ci_repair_approvals ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.github_ci_repair_approvals FORCE ROW LEVEL SECURITY;

CREATE FUNCTION browseai.audit_github_ci_repair()
RETURNS trigger LANGUAGE plpgsql VOLATILE SET search_path=pg_catalog,pg_temp AS $$
DECLARE t browseai.tasks%ROWTYPE; action_name text;
BEGIN
    SELECT * INTO STRICT t FROM browseai.tasks WHERE tenant_id=NEW.tenant_id AND id=NEW.task_id;
    IF TG_OP='INSERT' THEN
        IF t.state<>'WAIT_CI' OR t.revision<>NEW.task_revision OR NEW.repair_at IS NOT NULL
            THEN RAISE EXCEPTION 'invalid failure audit state'; END IF;
        INSERT INTO browseai.github_ci_repair_audit(failure_id,action,task_revision,target_state,repairs)
        VALUES(NEW.id,'failure_recorded',t.revision+1,'CI_FAILED',t.repairs);
    ELSE
        IF (to_jsonb(OLD)-'repair_at')<>(to_jsonb(NEW)-'repair_at') OR OLD.repair_at IS NOT NULL OR NEW.repair_at IS NULL
           OR t.revision<>NEW.task_revision+2 OR t.state NOT IN ('AWAIT_APPROVAL','NEED_HUMAN')
            THEN RAISE EXCEPTION 'invalid repair audit state'; END IF;
        action_name:=CASE WHEN t.state='AWAIT_APPROVAL' THEN 'repair_reserved' ELSE 'repair_exhausted' END;
        INSERT INTO browseai.github_ci_repair_audit(failure_id,action,task_revision,target_state,repairs)
        VALUES(NEW.id,action_name,t.revision,t.state,t.repairs);
    END IF;
    RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION browseai.audit_github_ci_repair() FROM PUBLIC;
CREATE TRIGGER github_ci_repair_audit_trigger AFTER INSERT OR UPDATE ON browseai.github_ci_failures
FOR EACH ROW EXECUTE FUNCTION browseai.audit_github_ci_repair();

-- Explicit human acceptance of exact repair bytes, not a send receipt.
-- Trusted caller must authenticate the actor and present the reviewed bytes.
-- The primitive never accepts an unreviewed monitor result as approval.
CREATE FUNCTION browseai.approve_github_ci_repair(p_tenant uuid,p_task uuid,p_revision bigint,p_failure uuid,
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
      AND l.expires_at>clock_timestamp() AND r.enabled AND i.enabled FOR SHARE OF l,p,r,i;
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
REVOKE ALL ON FUNCTION browseai.approve_github_ci_repair(uuid,uuid,bigint,uuid,uuid,uuid,bigint,bytea) FROM PUBLIC;
COMMIT;
