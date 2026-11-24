-- 0011_catalog_search_indexes.up.sql
-- The real catalog has ~65,000 products and ~6,000 brands. Two indexes keep
-- the admin screens fast with that volume:
--
-- 1. Admin list order (ListAll): ORDER BY is_active DESC, name [, sku]
--    LIMIT 50. With an index in the same order Postgres reads the first 50
--    entries and stops, instead of sorting the whole table on every page.
-- 2. Search "contains" (name ILIKE '%queso%'): a plain B-tree cannot help a
--    pattern that starts with %. pg_trgm splits the text in 3-letter pieces
--    ("que", "ues", "eso") and a GIN index finds the rows that have them all.
--    It works for searches of 3 or more characters.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX idx_products_admin_list ON products (is_active DESC, name, sku);
CREATE INDEX idx_brands_admin_list   ON brands (is_active DESC, name);

CREATE INDEX idx_products_name_trgm ON products USING gin (name gin_trgm_ops);
CREATE INDEX idx_products_sku_trgm  ON products USING gin (sku gin_trgm_ops);
CREATE INDEX idx_brands_name_trgm   ON brands   USING gin (name gin_trgm_ops);
