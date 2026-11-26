-- 0015_stock_count_sectors.up.sql
-- F-09 (stock count, also the initial inventory) needs a few changes to the
-- 0002 tables:
--
--   * created_by accepts NULL until there is a login (F-13), same as 0013.
--   * One DRAFT count per store at a time: the clerk always continues the
--     open one.
--   * A product can sit in several places (shelf and storeroom). Each load
--     goes to stock_count_entries with its sector; stock_count_items keeps
--     one row per product with the system snapshot and the counted total
--     (the sum of its entries, kept by the app in the same transaction).
--   * Optional cost (for the average cost, F-04) and expiry (for the batch a
--     positive difference creates) per counted product.
--   * Discarding a DRAFT deletes its items and entries (CASCADE). A
--     CONFIRMED count cannot be deleted: its stock_movements reference it.

ALTER TABLE stock_counts ALTER COLUMN created_by DROP NOT NULL;

CREATE UNIQUE INDEX stock_counts_one_draft_per_store
    ON stock_counts (store_id) WHERE status = 'DRAFT';

ALTER TABLE stock_count_items
    DROP CONSTRAINT stock_count_items_count_id_fkey,
    ADD CONSTRAINT stock_count_items_count_id_fkey
        FOREIGN KEY (count_id) REFERENCES stock_counts(id) ON DELETE CASCADE,
    ADD CONSTRAINT stock_count_items_counted_quantity_check CHECK (counted_quantity >= 0),
    ADD COLUMN unit_cost NUMERIC(12,2) NULL
        CONSTRAINT stock_count_items_unit_cost_check CHECK (unit_cost > 0),
    ADD COLUMN expiry_date DATE NULL,
    ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT now();
CREATE TRIGGER trg_stock_count_items_updated_at BEFORE UPDATE ON stock_count_items
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- One row per load: product counted in a sector. Zero is a valid count ("I
-- looked here and there is none").
CREATE TABLE stock_count_entries (
    id         BIGSERIAL PRIMARY KEY,
    item_id    BIGINT NOT NULL REFERENCES stock_count_items(id) ON DELETE CASCADE,
    sector     VARCHAR(60) NOT NULL CHECK (btrim(sector) <> ''),
    quantity   NUMERIC(12,3) NOT NULL CHECK (quantity >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_stock_count_entries_item_id ON stock_count_entries (item_id);
