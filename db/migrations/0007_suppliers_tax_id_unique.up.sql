-- 0007_suppliers_tax_id_unique.up.sql
-- A CUIT identifies one supplier: forbid duplicates. The column stays NULLable
-- (a supplier may not have a formal CUIT yet); UNIQUE allows many NULLs.
-- The app stores tax_id_number normalized to digits only (model.Supplier.Validate).

ALTER TABLE suppliers
    ADD CONSTRAINT suppliers_tax_id_number_key UNIQUE (tax_id_number);
