-- 0017_pos_sessions.down.sql
-- Fails if a session has no user (opened before the login): assign a user to
-- those rows first. Rolling back must not drop or invent data.

DELETE FROM app_settings WHERE key = 'max_sale_weight_kg';

DROP INDEX uq_cashier_sessions_one_open;

ALTER TABLE cashier_sessions ALTER COLUMN opened_by SET NOT NULL;
