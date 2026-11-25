-- 0014_product_shelf_life.up.sql
-- Days of life of a product (optional): how long it usually lasts from the
-- day it is received. Purchase reception uses it to prefill the expiry of a
-- batch (today + days); the clerk can change it. NULL = unknown (no prefill).
-- Per product, not per category: one category ("Fiambres") mixes things that
-- last 7 and 30 days.

ALTER TABLE products
    ADD COLUMN shelf_life_days INTEGER NULL
        CONSTRAINT products_shelf_life_days_range CHECK (shelf_life_days BETWEEN 1 AND 3650);
