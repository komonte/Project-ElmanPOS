-- 0026_sell_on_account.down.sql
-- Drops the customer of the sales. The ACCOUNT value stays in the enum:
-- Postgres cannot drop an enum value, and payments may use it.
DROP INDEX idx_sales_customer_id;
ALTER TABLE sales DROP COLUMN customer_id;
