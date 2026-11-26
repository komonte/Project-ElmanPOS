-- 0014_product_shelf_life.down.sql
-- Drops the column and its values: they are only a prefill, nothing else
-- depends on them.

ALTER TABLE products DROP COLUMN shelf_life_days;
