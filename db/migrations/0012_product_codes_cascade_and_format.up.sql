-- 0012_product_codes_cascade_and_format.up.sql
-- F-02: product barcodes.
--
-- 1. Deleting a product also deletes its barcodes. A barcode is not history:
--    sales, purchases and movements point to the product, not to the code.
--    A product with history still cannot be deleted (those FKs stay strict).
-- 2. A stored code is digits only, left-padded to 13 (14 for DUN-14), the
--    same as model.NormalizeBarcode. The CHECK keeps a manual INSERT or a
--    future import from storing "9000001" next to "0000009000001": UNIQUE
--    would see two different codes for the same scan.

ALTER TABLE product_codes
    DROP CONSTRAINT product_codes_product_id_fkey,
    ADD CONSTRAINT product_codes_product_id_fkey
        FOREIGN KEY (product_id) REFERENCES products(id) ON DELETE CASCADE;

-- Codes stored before this rule ("9000001"): pad them like the app does. If
-- that makes two rows equal, or a code has non-digits, the migration fails
-- and the row has to be fixed by hand.
UPDATE product_codes
SET code = lpad(code, 13, '0')
WHERE code ~ '^[0-9]{1,12}$';

ALTER TABLE product_codes
    ADD CONSTRAINT product_codes_code_format CHECK (code ~ '^[0-9]{13,14}$');
