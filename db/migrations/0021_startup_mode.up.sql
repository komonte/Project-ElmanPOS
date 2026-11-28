-- 0021_startup_mode.up.sql
-- Startup mode: the store loads its stock little by little (shelf by shelf),
-- so for a while products are sold before they are counted.
--
-- 1. app_settings startup_mode (false by default). Only while it is on, a
--    sale can sell more than the system has. Off, the old rule holds: no
--    stock, no sale.
-- 2. sale_items.uncovered_quantity: how much of the line the system did not
--    have. Stock never goes below zero (stock_balances keeps its CHECK): the
--    covered part leaves as usual, with its SALE movements; the uncovered
--    part has no movement, because the system never had it.
-- 3. A line stays pending until a confirmed count takes its snapshot of the
--    product after the sale (the count then measures what is really left).
--    That is derived in the query, so nothing is written to resolve it, and
--    voided sales drop out by their status.

ALTER TABLE sale_items
    ADD COLUMN uncovered_quantity NUMERIC(12,3) NOT NULL DEFAULT 0,
    ADD CONSTRAINT sale_items_uncovered_quantity_check
        CHECK (uncovered_quantity >= 0 AND uncovered_quantity <= quantity);

CREATE INDEX idx_sale_items_uncovered ON sale_items (product_id) WHERE uncovered_quantity > 0;

INSERT INTO app_settings (key, value, description)
VALUES ('startup_mode', 'false'::jsonb,
        'Startup mode: sales may sell more than the system stock; the rest is listed to count')
ON CONFLICT (key) DO NOTHING;
