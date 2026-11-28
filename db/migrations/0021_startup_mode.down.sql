-- 0021_startup_mode.down.sql
-- Forgets startup mode and how much of each line was sold without stock.

DELETE FROM app_settings WHERE key = 'startup_mode';

DROP INDEX idx_sale_items_uncovered;

ALTER TABLE sale_items DROP COLUMN uncovered_quantity;
