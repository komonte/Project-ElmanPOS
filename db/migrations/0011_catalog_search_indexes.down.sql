-- 0011_catalog_search_indexes.down.sql
-- The pg_trgm extension stays: other objects could depend on it, and keeping
-- it installed is harmless.

DROP INDEX idx_brands_name_trgm;
DROP INDEX idx_products_sku_trgm;
DROP INDEX idx_products_name_trgm;
DROP INDEX idx_brands_admin_list;
DROP INDEX idx_products_admin_list;
