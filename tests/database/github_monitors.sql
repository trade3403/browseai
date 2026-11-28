-- Isolated CI database; use owner only to construct fixtures and simulate time.
BEGIN;
INSERT INTO browseai.projects(id,tenant_id,name,repository_id,repository,code_branch)
VALUES('00000000-0000-4000-8000-000000009101','00000000-0000-4000-8000-000000000001','monitor fixture',99101,'owner/monitor','feat/monitor');
INSERT INTO browseai.tasks(id,tenant_id,project_id,name,state,stage_count,code_sha)
VALUES('00000000-0000-4000-8000-000000009102','00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000009101','monitor fixture','WAIT_CI',1,repeat('a',40));
SELECT browseai.provision_github_binding('00000000-0000-4000-8000-000000000001',99102,99101,'owner/monitor',12,34);
DO $$
DECLARE m uuid; first_token uuid; second_token uuid; n int;
BEGIN
    IF has_function_privilege('browseai_app','browseai.claim_github_monitor()','EXECUTE')
       OR has_function_privilege('browseai_webhook','browseai.claim_github_monitor()','EXECUTE')
       OR has_table_privilege('browseai_monitor','browseai.github_monitors','SELECT,INSERT,UPDATE,DELETE') THEN
        RAISE EXCEPTION 'monitor role boundary failed';
    END IF;
    SET LOCAL ROLE browseai_monitor;
    IF browseai.enqueue_github_monitor('00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000009102',10,'push') IS NOT NULL THEN RAISE EXCEPTION 'foreign tenant accepted'; END IF;
    m:=browseai.enqueue_github_monitor('00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000009102',10,'push');
    IF m IS NULL THEN RAISE EXCEPTION 'enqueue failed'; END IF;
    IF browseai.enqueue_github_monitor('00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000009102',10,'push') IS NOT NULL THEN RAISE EXCEPTION 'duplicate target'; END IF;
    SELECT claim_token INTO first_token FROM browseai.claim_github_monitor() WHERE id=m;
    IF first_token IS NULL THEN RAISE EXCEPTION 'claim failed'; END IF;
    SELECT count(*) INTO n FROM browseai.claim_github_monitor();
    IF n<>0 THEN RAISE EXCEPTION 'duplicate claim'; END IF;
    RESET ROLE;
    UPDATE browseai.github_monitors SET claim_until=clock_timestamp()-interval '1 second' WHERE id=m;
    SET LOCAL ROLE browseai_monitor;
    SELECT claim_token INTO second_token FROM browseai.claim_github_monitor() WHERE id=m;
    IF second_token IS NULL OR second_token=first_token THEN RAISE EXCEPTION 'recovery failed'; END IF;
    IF browseai.publish_github_monitor(m,first_token,'passed',0,clock_timestamp()+interval '30 seconds') THEN RAISE EXCEPTION 'stale claim published'; END IF;
    IF NOT browseai.publish_github_monitor(m,second_token,'pending',0,clock_timestamp()+interval '30 seconds') THEN RAISE EXCEPTION 'publication failed'; END IF;
    RESET ROLE;
    UPDATE browseai.github_monitors SET next_at=clock_timestamp()-interval '1 second' WHERE id=m;
    SET LOCAL ROLE browseai_monitor;
    SELECT claim_token INTO second_token FROM browseai.claim_github_monitor() WHERE id=m;
    RESET ROLE;
    UPDATE browseai.tasks SET revision=revision+1 WHERE id='00000000-0000-4000-8000-000000009102';
    SET LOCAL ROLE browseai_monitor;
    IF browseai.publish_github_monitor(m,second_token,'passed',0,clock_timestamp()+interval '30 seconds') THEN RAISE EXCEPTION 'stale task accepted'; END IF;
    RESET ROLE;
END $$;
ROLLBACK;
