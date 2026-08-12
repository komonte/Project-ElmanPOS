-- db/seed.sql
-- Development seed, identical to docs/04-manual-sql-testing.md §4.
-- Expects a freshly migrated DB (make db-reset) so auto-IDs are deterministic (1, 2, 3, ...).

BEGIN;

-- Stores & register
INSERT INTO stores (name, address) VALUES ('Local Centro', 'Av. Siempre viva 742');
INSERT INTO cash_registers (store_id, name) VALUES (1, 'Caja 1');

-- Taxes & categories
INSERT INTO taxes (name, rate) VALUES ('IVA 21%', 21.00), ('IVA 10.5%', 10.50);
INSERT INTO categories (parent_id, name, tax_id) VALUES (NULL, 'Almacen', 1);
INSERT INTO categories (parent_id, name, tax_id) VALUES (1, 'Galletitas', 1);
INSERT INTO categories (parent_id, name, tax_id) VALUES (1, 'Fiambreria', 1);

-- Brands
INSERT INTO brands (name) VALUES ('Bagley'), ('Coca-Cola'), ('Sancor');

-- Supplier
INSERT INTO suppliers (name, tax_id_number, phone, payment_terms, is_active)
VALUES ('Distribuidora del Sur S.A.', '20-12345678-9', '+54 11 5555-0100', 'Contado', TRUE);

-- Users (hashes are placeholders; the app seed writes real bcrypt hashes)
INSERT INTO users (username, name, password_hash, role, is_active)
VALUES ('admin', 'Admin',   '$2a$10$placeholder-placeholder-placeholder', 'ADMIN',       TRUE),
       ('cajero','Cashier', '$2a$10$placeholder-placeholder-placeholder', 'CASHIER',     TRUE),
       ('stock', 'Stock',   '$2a$10$placeholder-placeholder-placeholder', 'STOCK_CLERK', TRUE);

-- Products (IDs 1, 2, 3 deterministically)
INSERT INTO products (sku, name, description, brand_id, category_id, measure_unit, sale_type,
                      controls_batches, reorder_point, max_stock, margin_percent,
                      preferred_supplier_id, is_active)
VALUES ('ORE-200G',  'Galletitas Oreo 200g', 'Paquete de galletitas', 1, 2, 'UNIT', 'UNIT',
        FALSE, 10, 50, 30.00, 1, TRUE),                             -- P1: no batch control
       ('COLA-1500', 'Soda Cola 1.5L',       'Botella PET 1.5 L',    2, 1, 'UNIT', 'UNIT',
        FALSE, 0, NULL, 0, NULL, TRUE),                              -- P2: no batch control
       ('MORTA-KG',  'Mortadela',            'A granel, precio por kg', 3, 3, 'KILOGRAM', 'WEIGHT',
        TRUE, 2, 10, 25.00, NULL, TRUE);                             -- P3: batch control + weight

-- Barcodes
INSERT INTO product_codes (product_id, code_type, code)
VALUES (1, 'EAN13', '7790070991234'),
       (2, 'EAN13', '7790895001619'),
       (3, 'INTERNAL', '9000001');

-- Prices (per kg for P3)
INSERT INTO prices (product_id, unit_price, valid_from)
VALUES (1, 1500.00, '2026-01-01T00:00:00Z'),
       (2, 1800.00, '2026-01-01T00:00:00Z'),
       (3, 6500.00, '2026-01-01T00:00:00Z');

-- Purchase 1: initial stock (P1 x8 @900, P2 x12 @1300, P3 x5 @4200 lot L-2601)
INSERT INTO purchases (supplier_id, voucher_number, status, total, received_at, received_by)
VALUES (1, 'FAC-0001-2026', 'RECEIVED', 43800.00, '2026-07-20T09:00:00Z', 3);   -- 8*900 + 12*1300 + 5*4200

INSERT INTO purchase_items (purchase_id, product_id, quantity, unit_cost, batch_id)
VALUES (1, 1, 8, 900.00, NULL),
       (1, 2, 12, 1300.00, NULL),
       (1, 3, 5, 4200.00, NULL);

INSERT INTO product_batches (product_id, batch_number, expiry_date, original_quantity,
                             remaining_quantity, unit_cost)
VALUES (3, 'L-2601', '2026-12-15', 5, 5, 4200.00);
UPDATE purchase_items SET batch_id = 1 WHERE purchase_id = 1 AND product_id = 3;

INSERT INTO stock_movements (product_id, store_id, batch_id, movement_type, quantity,
                             unit_cost, purchase_id, performed_by, notes)
VALUES (1, 1, NULL, 'PURCHASE', 8, 900.00,  1, 3, 'stock inicial'),
       (2, 1, NULL, 'PURCHASE', 12, 1300.00, 1, 3, 'stock inicial'),
       (3, 1, 1,    'PURCHASE', 5, 4200.00, 1, 3, 'stock inicial');

INSERT INTO stock_balances (product_id, store_id, quantity) VALUES (1, 1, 8), (2, 1, 12), (3, 1, 5);

INSERT INTO product_costs (product_id, unit_cost, avg_cost, valid_from)
VALUES (1, 900.00, 900.00,  '2026-07-20T09:00:00Z'),
       (2, 1300.00, 1300.00, '2026-07-20T09:00:00Z'),
       (3, 4200.00, 4200.00, '2026-07-20T09:00:00Z');

-- Purchase 2: second lot for P3, expiring soon (L-2475 x2 @4500)
INSERT INTO purchases (supplier_id, voucher_number, status, total, received_at, received_by)
VALUES (1, 'FAC-0002-2026', 'RECEIVED', 9000.00, '2026-07-25T09:00:00Z', 3);

INSERT INTO purchase_items (purchase_id, product_id, quantity, unit_cost, batch_id)
VALUES (2, 3, 2, 4500.00, NULL);

INSERT INTO product_batches (product_id, batch_number, expiry_date, original_quantity,
                             remaining_quantity, unit_cost)
VALUES (3, 'L-2475', '2026-08-20', 2, 2, 4500.00);
UPDATE purchase_items SET batch_id = 2 WHERE purchase_id = 2;

INSERT INTO stock_movements (product_id, store_id, batch_id, movement_type, quantity,
                             unit_cost, purchase_id, performed_by, notes)
VALUES (3, 1, 2, 'PURCHASE', 2, 4500.00, 2, 3, 'stock inicial');

UPDATE stock_balances SET quantity = quantity + 2 WHERE product_id = 3 AND store_id = 1;

INSERT INTO product_costs (product_id, unit_cost, avg_cost, valid_from)
VALUES (3, 4500.00, 4285.71, '2026-07-25T09:00:00Z');
-- avg_cost = (4200.00 * 5 + 4500.00 * 2) / 7 = 4285.71  (F-04 running average)

-- Open cashier session (BR-02 precondition for F-07)
INSERT INTO cashier_sessions (cash_register_id, opened_by, opening_amount, status)
VALUES (1, 2, 50000.00, 'OPEN');

-- app_settings (JSONB; R-02 reads expiry_warning_days)
INSERT INTO app_settings (key, value, description)
VALUES ('expiry_warning_days', '30'::jsonb, 'Days ahead to flag batches in R-02'),
       ('currency', '"ARS"'::jsonb, 'Display currency');

COMMIT;

-- Sanity: balances after seed
\echo '-- EXPECTED: Galletitas Oreo 200g = 8; Soda Cola 1.5L = 12; Mortadela = 7.000'
SELECT p.name, b.quantity FROM stock_balances b JOIN products p ON p.id = b.product_id
WHERE b.store_id = 1 ORDER BY b.product_id;
