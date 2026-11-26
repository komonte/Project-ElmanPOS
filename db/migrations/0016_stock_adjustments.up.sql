-- 0016_stock_adjustments.up.sql
-- F-09b: known exits (own consumption, damage, expired goods, other), logged
-- any day, without an open stock count. Every stock_movements row needs
-- exactly one source (sale, purchase or count); a known exit is none of
-- them, so it gets its own header row and the movements reference it.
--
-- One adjustment is one product and one quantity; it can be several
-- movements (one per batch it is taken from, FIFO or the batch chosen).
-- THEFT is not offered: an unexplained loss shows up in a count (COUNT).

CREATE TABLE stock_adjustments (
    id         BIGSERIAL PRIMARY KEY,
    store_id   BIGINT NOT NULL REFERENCES stores(id),
    product_id BIGINT NOT NULL REFERENCES products(id),
    reason     adjustment_reason NOT NULL
        CHECK (reason IN ('OWN_CONSUMPTION', 'DAMAGE', 'EXPIRATION', 'OTHER')),
    -- What left the store, positive (the movements carry the sign).
    quantity   NUMERIC(12,3) NOT NULL CHECK (quantity > 0),
    notes      VARCHAR(500),
    -- NULL until there is a login (F-13), like 0013.
    created_by BIGINT REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- "Other" has to say what happened.
    CONSTRAINT stock_adjustments_other_needs_notes
        CHECK (reason <> 'OTHER' OR btrim(COALESCE(notes, '')) <> '')
);
CREATE INDEX idx_stock_adjustments_store_created ON stock_adjustments (store_id, created_at DESC);
CREATE INDEX idx_stock_adjustments_product_created ON stock_adjustments (product_id, created_at);

-- Like the ledger it explains: rows are never edited or deleted. A mistake
-- is corrected with a stock count.
CREATE FUNCTION prevent_stock_adjustments_write() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'stock_adjustments is append-only: % is forbidden', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_stock_adjustments_append_only
BEFORE UPDATE OR DELETE ON stock_adjustments
FOR EACH ROW EXECUTE FUNCTION prevent_stock_adjustments_write();

-- The ledger gets the fourth source.
ALTER TABLE stock_movements
    ADD COLUMN adjustment_id BIGINT REFERENCES stock_adjustments(id),
    DROP CONSTRAINT stock_movements_check,
    ADD CONSTRAINT stock_movements_one_source
        CHECK (num_nonnulls(sale_id, purchase_id, count_id, adjustment_id) = 1),
    -- A known exit only takes stock out, as an ADJUSTMENT.
    ADD CONSTRAINT stock_movements_adjustment_is_exit
        CHECK (adjustment_id IS NULL OR (movement_type = 'ADJUSTMENT' AND quantity < 0));
CREATE INDEX idx_stock_movements_adjustment_id ON stock_movements (adjustment_id)
    WHERE adjustment_id IS NOT NULL;
