-- 0007_suppliers_tax_id_unique.down.sql

ALTER TABLE suppliers
    DROP CONSTRAINT suppliers_tax_id_number_key;
