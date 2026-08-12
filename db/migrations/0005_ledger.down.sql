-- 0005_ledger.down.sql

DROP TABLE stock_balances;
DROP TRIGGER trg_stock_movements_append_only ON stock_movements;
DROP FUNCTION prevent_stock_movements_write();
DROP TABLE stock_movements;
