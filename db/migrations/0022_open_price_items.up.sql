-- 0022_open_price_items.up.sql
-- "Unregistered item": something the register sells that is not in the
-- system yet (a code that does not scan, a loose product without a code).
-- The cashier does not create products mid-sale (duplicates, wrong VAT or
-- unit); instead the admin marks one or more products (for example
-- "Varios 21 %" and "Varios 10,5 %") as open price.
--
-- 1. products.open_price: the register types the price on each line. The
--    product has no stock of its own: a sale of it moves nothing. The VAT is
--    its category's, as always.
-- 2. sale_items.scanned_code: the code that was scanned and not found, so
--    whoever loads the catalog can create the real product later. NULL when
--    nothing was scanned.

ALTER TABLE products ADD COLUMN open_price BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE sale_items
    ADD COLUMN scanned_code VARCHAR(40) NULL
        CONSTRAINT sale_items_scanned_code_check CHECK (btrim(scanned_code) <> '');
