-- 0028_sale_returns.down.sql
-- The RETURN stock movements of returns stay (the ledger is append-only).
DROP TABLE sale_return_payments;
DROP TABLE sale_return_items;
DROP TABLE sale_returns;
