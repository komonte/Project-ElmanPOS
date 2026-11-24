-- 0008_master_tables_is_active.down.sql

ALTER TABLE cash_registers DROP COLUMN is_active;
ALTER TABLE stores         DROP COLUMN is_active;
ALTER TABLE categories     DROP COLUMN is_active;
