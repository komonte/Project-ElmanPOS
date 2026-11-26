-- 0015_stock_count_sectors.down.sql
-- Drops the sector loads, cost and expiry of every count (the counted total
-- stays in stock_count_items). Fails if a count has no user (saved before the
-- login): assign a user to those rows first, as in 0013.

DROP TABLE stock_count_entries;

DROP TRIGGER trg_stock_count_items_updated_at ON stock_count_items;
ALTER TABLE stock_count_items
    DROP COLUMN updated_at,
    DROP COLUMN expiry_date,
    DROP COLUMN unit_cost,
    DROP CONSTRAINT stock_count_items_counted_quantity_check,
    DROP CONSTRAINT stock_count_items_count_id_fkey,
    ADD CONSTRAINT stock_count_items_count_id_fkey
        FOREIGN KEY (count_id) REFERENCES stock_counts(id);

DROP INDEX stock_counts_one_draft_per_store;

ALTER TABLE stock_counts ALTER COLUMN created_by SET NOT NULL;
