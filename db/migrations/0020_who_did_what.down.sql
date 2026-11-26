-- 0020_who_did_what.down.sql
-- Forgets who closed each register and who charged each sale.

ALTER TABLE sales DROP COLUMN created_by;

ALTER TABLE cashier_sessions DROP COLUMN closed_by;
