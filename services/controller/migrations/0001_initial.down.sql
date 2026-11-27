-- Destructive rollback of this initial schema; use only on disposable databases
-- or with a verified backup and an explicitly approved rollback.
BEGIN;
DROP SCHEMA browseai CASCADE;
COMMIT;
