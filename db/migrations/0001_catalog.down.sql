-- 0001_catalog.down.sql

DROP TABLE product_costs;
DROP TABLE prices;
DROP TABLE product_components;
DROP TABLE product_codes;
DROP TABLE products;
DROP TABLE users;
DROP TABLE suppliers;
DROP TABLE brands;
DROP TABLE categories;
DROP TABLE taxes;
DROP INDEX IF EXISTS idx_taxes_name_unique_lower;
DROP TYPE user_role;
DROP TYPE measure_unit;
DROP TYPE product_code_type;
DROP TYPE product_sale_type;
