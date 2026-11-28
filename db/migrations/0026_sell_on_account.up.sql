-- 0026_sell_on_account.up.sql
-- Selling on account ("fiado"): a new payment method ACCOUNT and the
-- customer of the sale. A sale with an ACCOUNT payment names its customer
-- (checked by the app: the payments are another table). The debt is derived
-- from the COMPLETED sales, so voiding one takes it off by itself.
-- ACCOUNT is not money in the drawer: the blind close (F-12) does not ask
-- for it and it is never part of the expected cash.
ALTER TYPE payment_method ADD VALUE IF NOT EXISTS 'ACCOUNT';

ALTER TABLE sales ADD COLUMN customer_id BIGINT NULL REFERENCES customers(id);
CREATE INDEX idx_sales_customer_id ON sales (customer_id) WHERE customer_id IS NOT NULL;
