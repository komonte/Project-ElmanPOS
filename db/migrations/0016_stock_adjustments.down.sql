-- 0016_stock_adjustments.down.sql
-- Fails if a known exit was logged: its movements cannot be deleted
-- (append-only ledger), so the column and the table cannot go. Rolling back
-- must not drop stock history.

DROP INDEX idx_stock_movements_adjustment_id;
ALTER TABLE stock_movements
    DROP CONSTRAINT stock_movements_adjustment_is_exit,
    DROP CONSTRAINT stock_movements_one_source,
    ADD CONSTRAINT stock_movements_check CHECK (num_nonnulls(sale_id, purchase_id, count_id) = 1),
    DROP COLUMN adjustment_id;

DROP TRIGGER trg_stock_adjustments_append_only ON stock_adjustments;
DROP FUNCTION prevent_stock_adjustments_write();
DROP TABLE stock_adjustments;
