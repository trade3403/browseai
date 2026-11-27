BEGIN;
-- Older draft rows have no instruction or acceptance plan. They are retained
-- for inspection, but cannot enter READY until a complete plan is available.
ALTER TABLE browseai.stages
    ADD COLUMN instruction text,
    ADD COLUMN dependencies text[] NOT NULL DEFAULT ARRAY[]::text[],
    ADD COLUMN acceptance text[] NOT NULL DEFAULT ARRAY[]::text[],
    ADD CONSTRAINT stage_plan_shape CHECK (
        (instruction IS NULL AND cardinality(dependencies)=0 AND cardinality(acceptance)=0)
        OR (instruction IS NOT NULL AND octet_length(instruction) BETWEEN 1 AND 8192
            AND cardinality(dependencies) BETWEEN 0 AND 100
            AND cardinality(acceptance) BETWEEN 1 AND 50)
    );
COMMIT;
