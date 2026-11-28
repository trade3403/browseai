BEGIN;
INSERT INTO browseai.projects(id,tenant_id,name,repository_id,repository,code_branch)
VALUES('00000000-0000-4000-8000-000000009301','00000000-0000-4000-8000-000000000001','coord fixture',99301,'owner/coord','feat/coord');
INSERT INTO browseai.tasks(id,tenant_id,project_id,name,state,stage_count,code_sha)
VALUES('00000000-0000-4000-8000-000000009302','00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000009301','coord fixture','WAIT_CI',1,repeat('a',40));
SELECT browseai.provision_github_binding('00000000-0000-4000-8000-000000000001',99302,99301,'owner/coord',12,34);
DO $$
DECLARE a uuid; b uuid; claimed uuid; token uuid; n int; due timestamptz;
BEGIN
    SET LOCAL ROLE browseai_monitor;
    a:=browseai.enqueue_github_monitor('00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000009302',10,'push');
    b:=browseai.enqueue_github_monitor('00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000009302',10,'pull_request');
    SELECT id,claim_token INTO claimed,token FROM browseai.claim_github_monitor();
    IF claimed IS NULL THEN RAISE EXCEPTION 'claim missing'; END IF;
    SELECT count(*) INTO n FROM browseai.claim_github_monitor();
    IF n<>0 THEN RAISE EXCEPTION 'installation overlap'; END IF;
    IF NOT browseai.publish_github_monitor(claimed,token,'unknown',1,clock_timestamp()+interval '1 hour') THEN RAISE EXCEPTION 'publish failed'; END IF;
    SELECT count(*) INTO n FROM browseai.claim_github_monitor();
    IF n<>0 THEN RAISE EXCEPTION 'shared backoff bypassed'; END IF;
    RESET ROLE;
    UPDATE browseai.github_monitors SET next_at=clock_timestamp()+interval '30 minutes' WHERE id IN(a,b) AND id<>claimed;
    SET LOCAL ROLE browseai_webhook;
    IF browseai.record_github_delivery('00000000-0000-4000-8000-000000009303',decode(repeat('ab',32),'hex'),99302,99301,'push')<>'accepted' THEN RAISE EXCEPTION 'delivery rejected'; END IF;
    RESET ROLE;
    SELECT next_at INTO due FROM browseai.github_monitors WHERE id IN(a,b) AND id<>claimed;
    IF due>clock_timestamp()+interval '11 seconds' THEN RAISE EXCEPTION 'hint did not wake target'; END IF;
    IF NOT EXISTS(SELECT 1 FROM browseai.github_monitors WHERE id=claimed AND failures=1 AND next_at>clock_timestamp()+interval '50 minutes') THEN RAISE EXCEPTION 'hint bypassed target backoff'; END IF;
    UPDATE browseai.github_monitors SET next_at=clock_timestamp()-interval '1 second' WHERE id IN(a,b) AND id<>claimed;
    SET LOCAL ROLE browseai_monitor;
    SELECT count(*) INTO n FROM browseai.claim_github_monitor();
    IF n<>0 THEN RAISE EXCEPTION 'hint bypassed installation backoff'; END IF;
    RESET ROLE;
    UPDATE browseai.github_installation_cooldowns SET next_at=clock_timestamp()-interval '1 second' WHERE installation_id=99302;
    SET LOCAL ROLE browseai_monitor;
    SELECT count(*) INTO n FROM browseai.claim_github_monitor();
    IF n<>1 THEN RAISE EXCEPTION 'cooldown recovery failed'; END IF;
    RESET ROLE;
END $$;
ROLLBACK;
