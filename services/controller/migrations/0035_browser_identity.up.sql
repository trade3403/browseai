BEGIN;
GRANT USAGE ON SCHEMA browseai TO browseai_browser_operator,browseai_browser_node;
CREATE TABLE browseai.browser_nodes (
    id uuid PRIMARY KEY,
    generation bigint NOT NULL CHECK(generation>0),
    credential_digest bytea NOT NULL UNIQUE CHECK(octet_length(credential_digest)=32),
    sequence bigint NOT NULL DEFAULT 0 CHECK(sequence>=0),
    capacity integer NOT NULL DEFAULT 1 CHECK(capacity BETWEEN 1 AND 256),
    occupied integer NOT NULL DEFAULT 0 CHECK(occupied BETWEEN 0 AND capacity),
    draining boolean NOT NULL DEFAULT true,
    received_at timestamptz,
    revoked_at timestamptz
);
CREATE TABLE browseai.browser_node_audit (
    operation_id uuid PRIMARY KEY,
    node_id uuid NOT NULL REFERENCES browseai.browser_nodes(id),
    generation bigint NOT NULL,
    operation text NOT NULL CHECK(operation IN ('register','rotate','revoke')),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
REVOKE ALL ON browseai.browser_nodes,browseai.browser_node_audit FROM PUBLIC;
ALTER TABLE browseai.browser_nodes ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.browser_nodes FORCE ROW LEVEL SECURITY;
ALTER TABLE browseai.browser_node_audit ENABLE ROW LEVEL SECURITY;
ALTER TABLE browseai.browser_node_audit FORCE ROW LEVEL SECURITY;
CREATE FUNCTION browseai.manage_browser_identity(p_node uuid,p_generation bigint,p_digest bytea,p_operation uuid,p_action text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
BEGIN
    IF session_user<>'browseai_browser_operator' OR p_node IS NULL OR p_operation IS NULL
      OR p_generation IS NULL OR p_generation<=0 OR p_action IS NULL
      OR p_action NOT IN ('register','rotate','revoke') THEN RETURN false; END IF;
    IF p_action='revoke' THEN
        IF p_digest IS NOT NULL THEN RETURN false; END IF;
    ELSIF p_digest IS NULL OR octet_length(p_digest)<>32 THEN RETURN false;
    END IF;
    BEGIN
        IF p_action='register' THEN
            IF p_generation<>1 THEN RETURN false; END IF;
            INSERT INTO browseai.browser_nodes(id,generation,credential_digest) VALUES(p_node,1,p_digest);
        ELSIF p_action='rotate' THEN
            UPDATE browseai.browser_nodes SET generation=p_generation,credential_digest=p_digest,
              sequence=0,received_at=NULL,draining=true
            WHERE id=p_node AND generation=p_generation-1 AND revoked_at IS NULL AND credential_digest<>p_digest;
            IF NOT FOUND THEN RETURN false; END IF;
        ELSE
            UPDATE browseai.browser_nodes SET revoked_at=clock_timestamp(),draining=true
            WHERE id=p_node AND generation=p_generation AND revoked_at IS NULL;
            IF NOT FOUND THEN RETURN false; END IF;
        END IF;
        INSERT INTO browseai.browser_node_audit(operation_id,node_id,generation,operation)
          VALUES(p_operation,p_node,p_generation,p_action);
        RETURN true;
    EXCEPTION WHEN unique_violation THEN RETURN false;
    END;
END $$;
CREATE FUNCTION browseai.record_browser_heartbeat(p_node uuid,p_generation bigint,p_digest bytea,p_sequence bigint,p_capacity integer,p_occupied integer)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path=pg_catalog,pg_temp AS $$
DECLARE n timestamptz:=clock_timestamp();
BEGIN
    IF session_user<>'browseai_browser_node' OR p_digest IS NULL OR octet_length(p_digest)<>32
      OR p_generation IS NULL OR p_generation<=0 OR p_sequence IS NULL OR p_sequence<=0
      OR p_capacity IS NULL OR p_capacity NOT BETWEEN 1 AND 256 OR p_occupied IS NULL
      OR p_occupied NOT BETWEEN 0 AND p_capacity THEN RETURN false; END IF;
    UPDATE browseai.browser_nodes SET sequence=p_sequence,capacity=p_capacity,occupied=p_occupied,received_at=n
    WHERE id=p_node AND generation=p_generation AND credential_digest=p_digest AND revoked_at IS NULL
      AND sequence<p_sequence AND (received_at IS NULL OR received_at<=n);
    RETURN FOUND;
END $$;
REVOKE ALL ON FUNCTION browseai.manage_browser_identity(uuid,bigint,bytea,uuid,text),browseai.record_browser_heartbeat(uuid,bigint,bytea,bigint,integer,integer) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION browseai.manage_browser_identity(uuid,bigint,bytea,uuid,text) TO browseai_browser_operator;
GRANT EXECUTE ON FUNCTION browseai.record_browser_heartbeat(uuid,bigint,bytea,bigint,integer,integer) TO browseai_browser_node;
COMMIT;
