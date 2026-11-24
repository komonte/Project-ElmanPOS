-- 0010_products_sku_sequence.down.sql

ALTER TABLE products ALTER COLUMN sku DROP DEFAULT;
DROP FUNCTION next_product_sku();
DROP SEQUENCE products_sku_seq;
