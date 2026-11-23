-- 0008_master_tables_is_active.up.sql
-- D-04 / BR-06: every master table supports soft delete via is_active.
-- 02-data-model.md already documents the column for these three tables,
-- but 0001/0002 created them without it. Existing rows become active.

ALTER TABLE categories     ADD COLUMN is_active BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE stores         ADD COLUMN is_active BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE cash_registers ADD COLUMN is_active BOOLEAN NOT NULL DEFAULT TRUE;
