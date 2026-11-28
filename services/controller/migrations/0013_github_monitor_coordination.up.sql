BEGIN;
CREATE TABLE browseai.github_installation_cooldowns (
    installation_id bigint PRIMARY KEY REFERENCES browseai.github_installations(installation_id),
    next_at timestamptz NOT NULL
);
REVOKE ALL ON browseai.github_installation_cooldowns FROM PUBLIC;
CREATE INDEX github_monitors_installation_idx ON browseai.github_monitors(installation_id,claim_until);
CREATE INDEX github_monitors_repository_idx ON browseai.github_monitors(tenant_id,installation_id,repository_id);
ALTER FUNCTION browseai.claim_github_monitor() RENAME TO claim_github_monitor_v1;
ALTER FUNCTION browseai.publish_github_monitor(uuid,uuid,text,integer,timestamptz) RENAME TO publish_github_monitor_v1;
REVOKE ALL ON FUNCTION browseai.claim_github_monitor_v1(),browseai.publish_github_monitor_v1(uuid,uuid,text,integer,timestamptz) FROM browseai_monitor;

-- Transaction-scoped advisory locks serialize claims and publication for
-- one installation. No lock or transaction is held during HTTP requests.
CREATE FUNCTION browseai.claim_github_monitor()
RETURNS SETOF browseai.github_monitors LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE target bigint;
BEGIN
    FOR target IN
        SELECT m.installation_id FROM browseai.github_monitors m
        WHERE m.next_at<=clock_timestamp() AND m.expires_at>clock_timestamp() AND m.failures<8
          AND (m.claim_until IS NULL OR m.claim_until<=clock_timestamp())
          AND NOT EXISTS(SELECT 1 FROM browseai.github_installation_cooldowns c WHERE c.installation_id=m.installation_id AND c.next_at>clock_timestamp())
          AND NOT EXISTS(SELECT 1 FROM browseai.github_monitors a WHERE a.installation_id=m.installation_id AND a.claim_until>clock_timestamp())
        GROUP BY m.installation_id ORDER BY min(m.next_at),m.installation_id LIMIT 32
    LOOP
        IF NOT pg_try_advisory_xact_lock(hashtextextended('browseai-monitor:'||target::text,0)) THEN CONTINUE; END IF;
        -- Fresh statement after obtaining the installation lock.
        IF EXISTS(SELECT 1 FROM browseai.github_installation_cooldowns c WHERE c.installation_id=target AND c.next_at>clock_timestamp())
           OR EXISTS(SELECT 1 FROM browseai.github_monitors a WHERE a.installation_id=target AND a.claim_until>clock_timestamp()) THEN CONTINUE; END IF;
        RETURN QUERY
        WITH candidate AS (
            SELECT m.id FROM browseai.github_monitors m
            JOIN browseai.tasks t ON t.tenant_id=m.tenant_id AND t.id=m.task_id
            JOIN browseai.tenants w ON w.id=m.tenant_id
            JOIN browseai.github_installations i ON i.tenant_id=m.tenant_id AND i.installation_id=m.installation_id
            JOIN browseai.github_repositories r ON r.tenant_id=m.tenant_id AND r.repository_id=m.repository_id AND r.installation_id=m.installation_id
            WHERE m.installation_id=target AND m.next_at<=clock_timestamp() AND m.expires_at>clock_timestamp() AND m.failures<8
              AND (m.claim_until IS NULL OR m.claim_until<=clock_timestamp())
              AND t.state='WAIT_CI' AND t.revision=m.task_revision AND t.code_sha=m.code_sha AND w.enabled AND i.enabled AND r.enabled
            ORDER BY m.next_at,m.id LIMIT 1 FOR UPDATE OF m SKIP LOCKED
        )
        UPDATE browseai.github_monitors m SET claim_token=gen_random_uuid(),claim_until=clock_timestamp()+interval '120 seconds',
            state='unknown',observed_at=NULL FROM candidate c WHERE m.id=c.id RETURNING m.*;
        IF FOUND THEN RETURN; END IF;
    END LOOP;
END $$;

CREATE FUNCTION browseai.publish_github_monitor(p_id uuid,p_token uuid,p_state text,p_failures integer,p_next timestamptz)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE target bigint; cooldown timestamptz;
BEGIN
    SELECT installation_id INTO target FROM browseai.github_monitors WHERE id=p_id;
    IF NOT FOUND THEN RETURN false; END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('browseai-monitor:'||target::text,0));
    IF NOT browseai.publish_github_monitor_v1(p_id,p_token,p_state,p_failures,p_next) THEN RETURN false; END IF;
    -- Any unknown read may be a rate limit. Conservatively share the full
    -- persisted delay, including an upstream Retry-After deadline.
    cooldown:=CASE WHEN p_state='unknown' THEN p_next ELSE clock_timestamp()+interval '5 seconds' END;
    INSERT INTO browseai.github_installation_cooldowns(installation_id,next_at) VALUES(target,cooldown)
    ON CONFLICT(installation_id) DO UPDATE SET next_at=greatest(browseai.github_installation_cooldowns.next_at,excluded.next_at);
    RETURN true;
END $$;
REVOKE ALL ON FUNCTION browseai.claim_github_monitor(),browseai.publish_github_monitor(uuid,uuid,text,integer,timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.claim_github_monitor(),browseai.publish_github_monitor(uuid,uuid,text,integer,timestamptz) TO browseai_monitor;

-- Only newly accepted ledger entries can wake a target. Hints never reset
-- failures, extend expiry, change task state, or bypass installation cooldown.
CREATE FUNCTION browseai.wake_github_monitors()
RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    UPDATE browseai.github_monitors m SET next_at=least(m.next_at,clock_timestamp()+interval '10 seconds')
    WHERE m.tenant_id=NEW.tenant_id AND m.installation_id=NEW.installation_id AND m.repository_id=NEW.repository_id
      AND m.failures=0 AND m.claim_token IS NULL AND m.expires_at>clock_timestamp()
      AND m.next_at>clock_timestamp()+interval '10 seconds'
      AND EXISTS(SELECT 1 FROM browseai.tasks t WHERE t.tenant_id=m.tenant_id AND t.id=m.task_id
        AND t.state='WAIT_CI' AND t.revision=m.task_revision AND t.code_sha=m.code_sha);
    RETURN NEW;
END $$;
REVOKE ALL ON FUNCTION browseai.wake_github_monitors() FROM PUBLIC;
CREATE TRIGGER github_delivery_wakeup AFTER INSERT ON browseai.github_deliveries
FOR EACH ROW EXECUTE FUNCTION browseai.wake_github_monitors();
COMMIT;
