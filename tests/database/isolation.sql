BEGIN;
SET LOCAL ROLE browseai_app;
DO $$
BEGIN
    IF (SELECT count(*) FROM browseai.projects) <> 0 OR
       (SELECT count(*) FROM browseai.tasks) <> 0 THEN
        RAISE EXCEPTION 'missing tenant did not fail closed';
    END IF;
END $$;
SELECT set_config('browseai.tenant_id','00000000-0000-4000-8000-000000000001',true);
DO $$
DECLARE n integer;
BEGIN
    IF (SELECT count(*) FROM browseai.projects) <> 1 OR
       (SELECT count(*) FROM browseai.tasks) <> 1 OR
       EXISTS (SELECT 1 FROM browseai.tasks WHERE name = 'Task B') THEN
        RAISE EXCEPTION 'tenant A read isolation failed';
    END IF;
    UPDATE browseai.tasks SET name = 'stolen' WHERE id = '00000000-0000-4000-8000-000000000022';
    GET DIAGNOSTICS n = ROW_COUNT;
    IF n <> 0 THEN RAISE EXCEPTION 'cross-tenant update'; END IF;
    DELETE FROM browseai.projects WHERE id = '00000000-0000-4000-8000-000000000012';
    GET DIAGNOSTICS n = ROW_COUNT;
    IF n <> 0 THEN RAISE EXCEPTION 'cross-tenant delete'; END IF;
    BEGIN
        INSERT INTO browseai.tasks(id,tenant_id,project_id,name,stage_count)
        VALUES ('00000000-0000-4000-8000-000000000023','00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000012','Injected',1);
        RAISE EXCEPTION 'cross-tenant insert accepted';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
    BEGIN
        INSERT INTO browseai.tasks(id,tenant_id,project_id,name,stage_count)
        VALUES ('00000000-0000-4000-8000-000000000024','00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000012','Injected',1);
        RAISE EXCEPTION 'cross-tenant foreign key accepted';
    EXCEPTION WHEN foreign_key_violation THEN NULL;
    END;
    BEGIN
        PERFORM token_digest FROM browseai.auth_sessions;
        RAISE EXCEPTION 'task role can access session digests';
    EXCEPTION WHEN insufficient_privilege THEN NULL;
    END;
    BEGIN
        UPDATE browseai.tasks SET state='COMPLETED' WHERE name='Task A';
        RAISE EXCEPTION 'invalid completion accepted';
    EXCEPTION WHEN check_violation THEN NULL;
    END;
    UPDATE browseai.tasks SET revision=revision+1 WHERE name='Task A' AND revision=0;
    GET DIAGNOSTICS n = ROW_COUNT;
    IF n <> 1 THEN RAISE EXCEPTION 'own update failed'; END IF;
    UPDATE browseai.tasks SET revision=revision+1 WHERE name='Task A' AND revision=0;
    GET DIAGNOSTICS n = ROW_COUNT;
    IF n <> 0 THEN RAISE EXCEPTION 'stale revision write accepted'; END IF;
END $$;
COMMIT;

BEGIN;
SET LOCAL ROLE browseai_app;
DO $$
BEGIN
    IF (SELECT count(*) FROM browseai.tasks) <> 0 THEN
        RAISE EXCEPTION 'tenant context leaked across transactions';
    END IF;
END $$;
SELECT set_config('browseai.tenant_id','00000000-0000-4000-8000-000000000002',true);
DO $$
BEGIN
    IF (SELECT count(*) FROM browseai.tasks) <> 1 OR
       NOT EXISTS (SELECT 1 FROM browseai.tasks WHERE name='Task B' AND revision=0) THEN
        RAISE EXCEPTION 'tenant B isolation failed';
    END IF;
END $$;
COMMIT;
