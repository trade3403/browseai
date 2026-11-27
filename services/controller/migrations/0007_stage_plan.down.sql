BEGIN;
ALTER TABLE browseai.stages DROP CONSTRAINT stage_plan_shape,
    DROP COLUMN acceptance, DROP COLUMN dependencies, DROP COLUMN instruction;
COMMIT;
