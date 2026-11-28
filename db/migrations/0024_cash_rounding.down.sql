-- 0024_cash_rounding.down.sql
-- Forgets the setting. Past sales keep their discount_amount.
DELETE FROM app_settings WHERE key = 'cash_rounding_multiple';
