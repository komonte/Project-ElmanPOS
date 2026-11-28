-- 0022_open_price_items.down.sql
-- Forgets open-price products and the codes scanned on their lines.

ALTER TABLE sale_items DROP COLUMN scanned_code;

ALTER TABLE products DROP COLUMN open_price;
