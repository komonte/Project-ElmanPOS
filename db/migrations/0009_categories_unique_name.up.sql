-- 0009_categories_unique_name.up.sql
-- 02 4.2: a category name cannot repeat among its siblings (same parent),
-- ignoring case ("Jabones" = "JABONES"). The same name may live under
-- different parents ("Varios" in Almacen and in Limpieza).
-- NULLS NOT DISTINCT (PG15+) makes root categories (parent_id NULL) count
-- as siblings too; without it two root "Limpieza" rows would be allowed.

CREATE UNIQUE INDEX idx_categories_parent_name_unique_lower
    ON categories (parent_id, LOWER(name)) NULLS NOT DISTINCT;

-- A category cannot be its own parent. Longer cycles (A -> B -> A) need
-- to walk the tree, so the app checks them when updating.
ALTER TABLE categories
    ADD CONSTRAINT chk_categories_not_own_parent CHECK (parent_id <> id);
