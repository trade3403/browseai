BEGIN;
INSERT INTO browseai.projects(id,tenant_id,name,repository_id,repository,code_branch)
VALUES('00000000-0000-4000-8000-000000009961','00000000-0000-4000-8000-000000000001','evidence fixture',99601,'owner/evidence','feat/evidence');
INSERT INTO browseai.tasks(id,tenant_id,project_id,name,state,stage_count,code_sha)
VALUES('00000000-0000-4000-8000-000000009962','00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000009961','evidence fixture','WAIT_CI',1,repeat('a',40));
SELECT browseai.provision_github_binding('00000000-0000-4000-8000-000000000001',99602,99601,'owner/evidence',12,34);
DO $$
DECLARE
    e browseai.github_ci_evidence%ROWTYPE;
    bad browseai.github_ci_evidence%ROWTYPE;
    n timestamptz:=clock_timestamp();
    role_name text;
BEGIN
    e.id:=gen_random_uuid();
    e.tenant_id:='00000000-0000-4000-8000-000000000001';
    e.project_id:='00000000-0000-4000-8000-000000009961';
    e.task_id:='00000000-0000-4000-8000-000000009962';
    e.task_revision:=0;
    e.repository_id:=99601;
    e.installation_id:=99602;
    e.code_branch:='feat/evidence';
    e.head_sha:=repeat('a',40);
    e.event_type:='push';
    e.workflow_id:=10;
    e.workflow_name:='CI';
    e.workflow_path:='.github/workflows/ci.yml';
    e.source_digest:=repeat('b',64);
    e.run_id:=100;
    e.attempt:=1;
    e.job_ids:=ARRAY[101,102]::bigint[];
    e.required_jobs:=ARRAY['gate'];
    e.verifier_id:=gen_random_uuid();
    e.key_id:=gen_random_uuid();
    e.statement:=convert_to('synthetic SQL constraint fixture','UTF8');
    e.signature:=decode(repeat('ab',64),'hex');
    e.evidence_digest:=sha256(e.statement||e.signature);
    e.observed_at:=n;
    e.issued_at:=n;
    e.expires_at:=n+interval '25 seconds';
    e.created_at:=n;
    PERFORM browseai.register_github_verifier(e.tenant_id,e.installation_id,e.repository_id,e.verifier_id,e.key_id,decode(repeat('bc',32),'hex'),n-interval '1 minute',n+interval '1 day',gen_random_uuid());

    FOREACH role_name IN ARRAY ARRAY['browseai_app','browseai_auth','browseai_admin','browseai_monitor','browseai_webhook','browseai_github_operator','browseai_verifier_operator','browseai_verifier_reader'] LOOP
        IF has_table_privilege(role_name,'browseai.github_ci_evidence','SELECT,INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER') THEN
            RAISE EXCEPTION 'unexpected evidence access for %',role_name;
        END IF;
    END LOOP;
    -- Owner-only fixture proves constraints, never represents actual attestation.
    INSERT INTO browseai.github_ci_evidence SELECT (e).*;
    bad:=e;
    bad.id:=gen_random_uuid();
    BEGIN
        INSERT INTO browseai.github_ci_evidence SELECT (bad).*;
        RAISE EXCEPTION 'duplicate accepted';
    EXCEPTION WHEN unique_violation THEN NULL; END;
    bad.signature:=decode(repeat('ac',64),'hex');
    bad.evidence_digest:=sha256(bad.statement||bad.signature);
    BEGIN
        INSERT INTO browseai.github_ci_evidence SELECT (bad).*;
        RAISE EXCEPTION 'duplicate run accepted';
    EXCEPTION WHEN unique_violation THEN NULL; END;
    bad.run_id:=200;
    bad.evidence_digest:=decode(repeat('00',32),'hex');
    BEGIN
        INSERT INTO browseai.github_ci_evidence SELECT (bad).*;
        RAISE EXCEPTION 'digest mismatch accepted';
    EXCEPTION WHEN check_violation THEN NULL; END;
    bad.evidence_digest:=sha256(bad.statement||bad.signature);
    bad.event_type:='pull_request';
    BEGIN
        INSERT INTO browseai.github_ci_evidence SELECT (bad).*;
        RAISE EXCEPTION 'missing PR identity accepted';
    EXCEPTION WHEN check_violation THEN NULL; END;
    bad.base_branch:='main';
    bad.base_sha:=repeat('c',40);
    bad.merge_sha:=repeat('d',40);
    bad.pr_number:=3;
    bad.job_ids:=ARRAY[101,101]::bigint[];
    BEGIN
        INSERT INTO browseai.github_ci_evidence SELECT (bad).*;
        RAISE EXCEPTION 'duplicate jobs accepted';
    EXCEPTION WHEN raise_exception THEN
        IF SQLERRM<>'evidence scope or freshness unavailable' THEN RAISE; END IF;
    END;
    bad.job_ids:=e.job_ids;
    bad.tenant_id:='00000000-0000-4000-8000-000000000002';
    BEGIN
        INSERT INTO browseai.github_ci_evidence SELECT (bad).*;
        RAISE EXCEPTION 'cross tenant accepted';
    EXCEPTION WHEN raise_exception THEN
        IF SQLERRM<>'evidence scope or freshness unavailable' THEN RAISE; END IF;
    END;
    bad.tenant_id:=e.tenant_id;
    bad.expires_at:=n-interval '1 second';
    BEGIN
        INSERT INTO browseai.github_ci_evidence SELECT (bad).*;
        RAISE EXCEPTION 'expired evidence accepted';
    EXCEPTION WHEN raise_exception THEN
        IF SQLERRM<>'evidence scope or freshness unavailable' THEN RAISE; END IF;
    END;
    bad.expires_at:=e.expires_at;
    INSERT INTO browseai.github_ci_evidence SELECT (bad).*;
    bad.id:=gen_random_uuid();
    bad.run_id:=201;
    bad.signature:=decode(repeat('ad',64),'hex');
    bad.evidence_digest:=sha256(bad.statement||bad.signature);
    UPDATE browseai.tasks SET revision=1 WHERE id=e.task_id;
    BEGIN
        INSERT INTO browseai.github_ci_evidence SELECT (bad).*;
        RAISE EXCEPTION 'stale revision accepted';
    EXCEPTION WHEN raise_exception THEN
        IF SQLERRM<>'evidence scope or freshness unavailable' THEN RAISE; END IF;
    END;
    bad.task_revision:=1;
    UPDATE browseai.github_repositories SET enabled=false WHERE tenant_id=e.tenant_id AND repository_id=e.repository_id;
    BEGIN
        INSERT INTO browseai.github_ci_evidence SELECT (bad).*;
        RAISE EXCEPTION 'disabled repository accepted';
    EXCEPTION WHEN raise_exception THEN
        IF SQLERRM<>'evidence scope or freshness unavailable' THEN RAISE; END IF;
    END;
    UPDATE browseai.github_repositories SET enabled=true WHERE tenant_id=e.tenant_id AND repository_id=e.repository_id;
    PERFORM browseai.revoke_github_verifier(e.tenant_id,e.installation_id,e.repository_id,e.key_id,gen_random_uuid());
    BEGIN
        INSERT INTO browseai.github_ci_evidence SELECT (bad).*;
        RAISE EXCEPTION 'revoked signer accepted';
    EXCEPTION WHEN raise_exception THEN
        IF SQLERRM<>'evidence scope or freshness unavailable' THEN RAISE; END IF;
    END;
    IF (SELECT count(*) FROM browseai.github_ci_evidence WHERE task_id=e.task_id)<>2 THEN
        RAISE EXCEPTION 'unexpected ledger count';
    END IF;
END $$;
ROLLBACK;
