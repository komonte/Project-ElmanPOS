-- 0020_who_did_what.up.sql
-- F-13 (login), PR E: record who did each thing.
--
-- 1. closed_by: who closed the register (F-12). opened_by alone is not
--    enough: the register is shared at shift change.
-- 2. sales.created_by: who charged the sale (F-07). For the same reason,
--    the session's opened_by does not say who sold each ticket.
-- 3. Who voided is not a column: audit_log (VOID, user_id) and the
--    performed_by of the stock movements already say it.
-- 4. Both accept NULL, like opened_by and performed_by (0013, 0017): rows
--    from before the login have no author, and the app does not invent one.
--    Every new write fills them.

ALTER TABLE cashier_sessions ADD COLUMN closed_by BIGINT REFERENCES users(id);

ALTER TABLE sales ADD COLUMN created_by BIGINT REFERENCES users(id);
