-- 0013_purchase_user_optional.down.sql
-- Fails if a row has no user (saved before the login): assign a user to those
-- rows first. Rolling back must not drop or invent data.

ALTER TABLE stock_movements ALTER COLUMN performed_by SET NOT NULL;
ALTER TABLE purchases ALTER COLUMN received_by SET NOT NULL;
