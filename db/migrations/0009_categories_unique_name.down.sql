-- 0009_categories_unique_name.down.sql

ALTER TABLE categories DROP CONSTRAINT chk_categories_not_own_parent;
DROP INDEX idx_categories_parent_name_unique_lower;
