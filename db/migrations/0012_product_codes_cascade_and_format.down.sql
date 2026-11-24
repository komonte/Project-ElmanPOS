-- 0012_product_codes_cascade_and_format.down.sql
-- Padded codes stay padded: they are still valid codes.

ALTER TABLE product_codes DROP CONSTRAINT product_codes_code_format;

ALTER TABLE product_codes
    DROP CONSTRAINT product_codes_product_id_fkey,
    ADD CONSTRAINT product_codes_product_id_fkey
        FOREIGN KEY (product_id) REFERENCES products(id);
