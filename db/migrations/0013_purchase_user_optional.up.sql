-- 0013_purchase_user_optional.up.sql
-- F-06 (purchase reception) before F-13 (login): there is no user yet to
-- record who received a purchase or made a stock movement. Same as
-- audit_log.user_id: the column accepts NULL until there is a login, and
-- the app does not invent an author (no fixed "system" user).
--
-- When F-13 lands, the app always fills these columns. Making them NOT NULL
-- again is a later decision: rows saved before the login stay NULL.

ALTER TABLE purchases ALTER COLUMN received_by DROP NOT NULL;
ALTER TABLE stock_movements ALTER COLUMN performed_by DROP NOT NULL;
