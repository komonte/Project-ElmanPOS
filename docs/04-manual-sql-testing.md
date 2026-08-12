# 04. Manual SQL Testing

## 1. Purpose & Scope

This document is a **human-executed verification checklist**. It translates the specifications in `02-data-model.md` and `03-flows-and-transactions.md` into concrete SQL that a developer runs against a live PostgreSQL database to **prove the schema and its transactions behave as specified** — before a single line of Go exists.

**What it verifies (DB-enforceable):**
- Schema: enums, FKs, CHECKs, UNIQUEs, indexes (§3).
- The transactional half of every core flow: F-06 reception, F-07 atomic sale, F-08 full void, F-09 stock count, F-12 cash close (§5).
- Cross-cutting invariants: ledger == balance, payments == total, no negative stock, FIFO allocation ↔ ledger (§6).
- Defense-in-depth guards: append-only trigger, FK protection, CHECK constraints (§7).
- Report smoke tests: R-01..R-07 run against real data (§8).

**What it does NOT verify (application layer — tested later with Go integration tests):**
- UI steps, barcode scan latency, weight-input screen, session/permission enforcement (F-13), PVP suggestion logic (F-04).
- Every report's business math is validated here; the HTTP/JSON layer is not.

**How to read each test:** every block carries `-- EXPECTED:` comments. A test **PASSES** when the output matches the expected line and every "What you should see" bullet. Any mismatch is a spec/DDL bug to fix **before** writing code.

**Conventions:**
- All commands run inside `psql`. `\gset` stores a `RETURNING` value into a psql variable (`:sale_id`) used by later statements — no sequence-name assumptions.
- Sections run **in order** from a fresh seed (§2 + §4), because later tests depend on earlier state.
- Money is `NUMERIC` (**BR-01**); prices include IVA (03 §1); floating point never appears.

---

## 2. Setup & Reset

Prerequisites: the migrations built 1:1 from `02-data-model.md` are applied, including the **append-only trigger** on `stock_movements` defined in 03 §6 (if it is missing, §7.1 is a RED FAIL and the migration is incomplete).

```bash
# Bring the stack up and apply migrations (Makefile targets defined in the scaffold step)
make db-up && make db-migrate-up

# Connect (adjust to your docker-compose service name / user / db)
psql -h localhost -U elman_u -d elman_u
```

**Reset** (restores a blank, migrated DB so the manual is fully re-runnable):

```bash
make db-reset        # canonical: drop schema -> re-migrate
```

Manual SQL alternative (same effect, from inside psql):

```sql
TRUNCATE app_settings, audit_log, cash_audits, cashier_sessions, payments, sale_item_batches,
         sale_items, sales, stock_movements, stock_balances, stock_count_items, stock_counts,
         purchase_items, purchases, product_batches, product_costs, prices, product_components,
         product_codes, products, cash_registers, stores, suppliers, brands, categories, taxes, users
RESTART IDENTITY CASCADE;
```

`RESTART IDENTITY` matters: the seed (§4) relies on deterministic auto-IDs (`1, 2, 3, ...`) so every later reference and every `EXPECTED` value is stable. **Never skip a reset between runs.**

---

## 3. Schema Verification

Run each check and confirm the expected result. If a constraint/index below is absent, the migration is not 1:1 with `02` — stop and fix it.

| # | Command | Expected |
|---|---|---|
| 3.1 | `\dt` | 27 tables (4.1..4.25 of `02`, all present) |
| 3.2 | `\dT+` | 11 enums: `product_sale_type`, `product_code_type`, `movement_type`, `adjustment_reason`, `payment_method`, `user_role`, `sale_status`, `session_status`, `count_status`, `purchase_status`, `measure_unit` |
| 3.3 | `\d stock_movements` | CHECK `num_nonnulls(sale_id, purchase_id, count_id) = 1`; CHECK sign-per-type; FKs to `products`, `stores`, `product_batches`, `sales`, `purchases`, `stock_counts`, `users`; indexes `(product_id, created_at)`, `(batch_id)`, `(store_id, created_at)`, `(movement_type)` |
| 3.4 | `\d stock_balances` | PK `(product_id, store_id)`; CHECK `quantity >= 0` |
| 3.5 | `\d product_batches` | CHECK `remaining_quantity >= 0 AND remaining_quantity <= original_quantity`; index `(product_id, remaining_quantity, expiry_date)` |
| 3.6 | `\d products` | UNIQUE `sku`; CHECKs `reorder_point >= 0`, `max_stock IS NULL OR max_stock > reorder_point`, `margin_percent >= 0` |
| 3.7 | `\d prices`, `\d product_costs` | UNIQUE `(product_id, valid_from)`; index `(product_id, valid_from DESC)` |
| 3.8 | `\d sales` | UNIQUE `(cashier_session_id, sale_number)`; CHECK `total > 0` |
| 3.9 | `\d sale_item_batches`, `\d payments`, `\d product_components` | `sale_item_batches` CHECK `quantity > 0`; `payments` CHECK `amount > 0`; `product_components` CHECK `pack_id <> component_id` |
| 3.10 | `\d app_settings` | PK `key`; `value` is `JSONB` |
| 3.11 | `\dy` | the append-only ledger trigger on `stock_movements` |
| 3.12 | `\d product_codes` | UNIQUE `code` (**NFR-02** / O(1) scan target) |

---

## 4. Seed Data

Run this block as a single transaction. It reproduces the "initial stock" of the store and the figures every `EXPECTED` below is computed from. `valid_from` uses **literal timestamps** because `now()` is constant inside one transaction and would collide with `UNIQUE(product_id, valid_from)`.

```sql
BEGIN;

-- 4.1 Stores & register
INSERT INTO stores (name, address) VALUES ('Local Centro', 'Av. Siempre viva 742');
INSERT INTO cash_registers (store_id, name) VALUES (1, 'Caja 1');

-- 4.2 Taxes & categories
INSERT INTO taxes (name, rate) VALUES ('IVA 21%', 21.00), ('IVA 10.5%', 10.50);
INSERT INTO categories (parent_id, name, tax_id) VALUES (NULL, 'Almacen', 1);
INSERT INTO categories (parent_id, name, tax_id) VALUES (1, 'Galletitas', 1);
INSERT INTO categories (parent_id, name, tax_id) VALUES (1, 'Fiambreria', 1);

-- 4.3 Brands
INSERT INTO brands (name) VALUES ('Bagley'), ('Coca-Cola'), ('Sancor');

-- 4.4 Supplier
INSERT INTO suppliers (name, tax_id_number, phone, payment_terms, is_active)
VALUES ('Distribuidora del Sur S.A.', '20-12345678-9', '+54 11 5555-0100', 'Contado', TRUE);

-- 4.5 Users (hashes are placeholders; the app seed writes real bcrypt hashes)
INSERT INTO users (username, name, password_hash, role, is_active)
VALUES ('admin', 'Admin',   '$2a$10$placeholder-placeholder-placeholder', 'ADMIN',       TRUE),
       ('cajero','Cashier', '$2a$10$placeholder-placeholder-placeholder', 'CASHIER',     TRUE),
       ('stock', 'Stock',   '$2a$10$placeholder-placeholder-placeholder', 'STOCK_CLERK', TRUE);

-- 4.6 Products (IDs 1, 2, 3 deterministically)
INSERT INTO products (sku, name, description, brand_id, category_id, measure_unit, sale_type,
                      controls_batches, reorder_point, max_stock, margin_percent,
                      preferred_supplier_id, is_active)
VALUES ('ORE-200G',  'Galletitas Oreo 200g', 'Paquete de galletitas', 1, 2, 'UNIT', 'UNIT',
        FALSE, 10, 50, 30.00, 1, TRUE),                             -- P1: no batch control
       ('COLA-1500', 'Soda Cola 1.5L',       'Botella PET 1.5 L',    2, 1, 'UNIT', 'UNIT',
        FALSE, 0, NULL, 0, NULL, TRUE),                              -- P2: no batch control
       ('MORTA-KG',  'Mortadela',            'A granel, precio por kg', 3, 3, 'KILOGRAM', 'WEIGHT',
        TRUE, 2, 10, 25.00, NULL, TRUE);                             -- P3: batch control + weight

-- 4.7 Barcodes
INSERT INTO product_codes (product_id, code_type, code)
VALUES (1, 'EAN13', '7790070991234'),
       (2, 'EAN13', '7790895001619'),
       (3, 'INTERNAL', '9000001');

-- 4.8 Prices (per kg for P3)
INSERT INTO prices (product_id, unit_price, valid_from)
VALUES (1, 1500.00, '2026-01-01T00:00:00Z'),
       (2, 1800.00, '2026-01-01T00:00:00Z'),
       (3, 6500.00, '2026-01-01T00:00:00Z');

-- 4.9 Purchase 1: initial stock (P1 x8 @900, P2 x12 @1300, P3 x5 @4200 lot L-2601)
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

-- 4.10 Purchase 2: second lot for P3, expiring soon (L-2475 x2 @4500)
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

-- 4.11 Open cashier session (BR-02 precondition for F-07)
INSERT INTO cashier_sessions (cash_register_id, opened_by, opening_amount, status)
VALUES (1, 2, 50000.00, 'OPEN');

-- 4.12 app_settings (JSONB; R-02 reads expiry_warning_days)
INSERT INTO app_settings (key, value, description)
VALUES ('expiry_warning_days', '30'::jsonb, 'Days ahead to flag batches in R-02'),
       ('currency', '"ARS"'::jsonb, 'Display currency');

COMMIT;
```

**What you should see:**
- Every statement succeeds; the seed is a single atomic transaction (**BR-04** applies to the app, but the seed behaves the same way).
- Stock after seed: **P1 = 8**, **P2 = 12**, **P3 = 7.000** (batch L-2475 = 2.000, batch L-2601 = 5.000).

```sql
-- Sanity: balances after seed
SELECT p.name, b.quantity FROM stock_balances b JOIN products p ON p.id = b.product_id
WHERE b.store_id = 1 ORDER BY b.product_id;
-- EXPECTED: Galletitas Oreo 200g = 8; Soda Cola 1.5L = 12; Mortadela = 7.000
```

---

## 5. Flow Tests

Run §5.1 → §5.5 in order. They are **stateful**: each builds on the previous. Rolled-back demo blocks (marked `BEGIN ... ROLLBACK`) leave no trace.

### 5.1 F-06: Purchase Reception (live)

Receives 6 units of P2 @ 1350.00 and recomputes the running average cost (F-04 formula).

```sql
BEGIN;
INSERT INTO purchases (supplier_id, voucher_number, status, total, received_at, received_by)
VALUES (1, 'FAC-0003-2026', 'RECEIVED', 8100.00, now(), 3);                       -- 6 * 1350.00

INSERT INTO purchase_items (purchase_id, product_id, quantity, unit_cost, batch_id)
SELECT id, 2, 6, 1350.00, NULL FROM purchases WHERE voucher_number = 'FAC-0003-2026';

INSERT INTO stock_movements (product_id, store_id, batch_id, movement_type, quantity,
                             unit_cost, purchase_id, performed_by)
SELECT 2, 1, NULL, 'PURCHASE', 6, 1350.00, id, 3 FROM purchases WHERE voucher_number = 'FAC-0003-2026';

UPDATE stock_balances SET quantity = quantity + 6 WHERE product_id = 2 AND store_id = 1;

INSERT INTO product_costs (product_id, unit_cost, avg_cost, valid_from)
VALUES (2, 1350.00, 1316.67, now());
-- avg_cost = (1300.00 * 12 + 1350.00 * 6) / 18 = 1316.67
COMMIT;
```

**What you should see:**
- P2 balance goes 12 → **18**; ledger gains one `PURCHASE` row; **no** `product_batches` row is created for P2 (batchless product, no batch friction — 03 F-06 edge case).
- Cost history for P2 is append-only: `(1300.00, 1300.00)` then `(1350.00, 1316.67)` (**BR-03**).

```sql
SELECT product_id, unit_cost, avg_cost, valid_from FROM product_costs WHERE product_id = 2 ORDER BY valid_from;
-- EXPECTED: two rows, avg 1316.67 on the latest
SELECT COUNT(*) AS p2_batches FROM product_batches WHERE product_id = 2;
-- EXPECTED: 0
```

### 5.2 F-07: Atomic Sale

#### 5.2.1 Sale A — atomic, with snapshots and FIFO (committed)

```sql
BEGIN;
-- BR-02 guard: session must be OPEN
SELECT id FROM cashier_sessions WHERE id = 1 AND status = 'OPEN' FOR UPDATE;
-- Lock touched balances in ASCENDING product_id order (anti-deadlock, 03 §6)
SELECT product_id FROM stock_balances WHERE store_id = 1 AND product_id IN (1, 3) ORDER BY product_id FOR UPDATE;

-- Show the FIFO candidate order BEFORE allocating (P-04: oldest expiry first)
SELECT id, batch_number, expiry_date, remaining_quantity
FROM product_batches WHERE product_id = 3 AND remaining_quantity > 0
ORDER BY expiry_date ASC NULLS FIRST, id ASC;
-- EXPECTED: L-2475 (2026-08-20) first, then L-2601 (2026-12-15)

INSERT INTO sales (cashier_session_id, sale_number, status, subtotal, tax_amount, discount_amount, total)
VALUES (1, 1, 'COMPLETED', 8000.00, 1388.43, 0.00, 8000.00)
RETURNING id AS sale_id \gset
-- subtotal = 1*1500 + 1*6500 = 8000; tax = 1500*21/121 + 6500*21/121 = 260.33 + 1128.10 = 1388.43

-- Line items with price/cost SNAPSHOTS (BR-03)
INSERT INTO sale_items (sale_id, product_id, quantity, unit_price, unit_cost, tax_rate)
VALUES (:sale_id, 1, 1,    1500.00,  900.00, 21.00),
       (:sale_id, 3, 1.000, 6500.00, 4500.00, 21.00);

-- FIFO allocation: 1.000 kg from batch L-2475 (P-04)
INSERT INTO sale_item_batches (sale_item_id, batch_id, quantity, unit_cost)
SELECT id, 2, 1.000, 4500.00 FROM sale_items WHERE sale_id = :sale_id AND product_id = 3;

UPDATE product_batches SET remaining_quantity = remaining_quantity - 1.000 WHERE id = 2;  -- 2.000 -> 1.000

INSERT INTO payments (sale_id, method, amount) VALUES (:sale_id, 'CASH', 8000.00);

-- Ledger: one SALE row per batch allocation (P-01 / BR-05); batchless P1 has batch_id NULL
INSERT INTO stock_movements (product_id, store_id, batch_id, movement_type, quantity,
                             unit_cost, sale_id, performed_by)
VALUES (1, 1, NULL, 'SALE', -1,    900.00, :sale_id, 2),
       (3, 1, 2,    'SALE', -1.000, 4500.00, :sale_id, 2);

UPDATE stock_balances SET quantity = quantity - 1     WHERE product_id = 1 AND store_id = 1;  -- 8 -> 7
UPDATE stock_balances SET quantity = quantity - 1.000 WHERE product_id = 3 AND store_id = 1;  -- 7.000 -> 6.000
COMMIT;
```

> **Spec note (header order):** 03 F-07 lists `sale_items` before the `sales` header, but `sale_items.sale_id` is a `NOT NULL` FK, so the header must be inserted first (or the FK made `DEFERRABLE`). This manual uses header-first; treat 03's ordering as conceptual, and mirror header-first in the Go transaction.

**What you should see:**
- `sale_items` store the **snapshots** (P1: 1500/900, P3: 6500/4500), not today's `prices`/`product_costs` values (**BR-03**).
- Batch L-2475 remaining 2.000 → **1.000**; `is_consumed` still FALSE.
- Balances: P1 **7**, P3 **6.000**. Ledger has two `SALE` rows.

```sql
SELECT si.product_id, si.quantity, si.unit_price, si.unit_cost FROM sale_items si WHERE si.sale_id = :sale_id;
-- EXPECTED: P1 1 / 1500.00 / 900.00 ; P3 1.000 / 6500.00 / 4500.00
SELECT remaining_quantity, is_consumed FROM product_batches WHERE id = 2;
-- EXPECTED: 1.000 / FALSE
```

#### 5.2.2 Sale B — a second, committed sale (the void target in §5.3)

```sql
BEGIN;
SELECT id FROM cashier_sessions WHERE id = 1 AND status = 'OPEN' FOR UPDATE;
SELECT product_id FROM stock_balances WHERE store_id = 1 AND product_id = 2 FOR UPDATE;

INSERT INTO sales (cashier_session_id, sale_number, status, subtotal, tax_amount, discount_amount, total)
VALUES (1, 2, 'COMPLETED', 3600.00, 624.79, 0.00, 3600.00)
RETURNING id AS sale_id \gset
-- tax = 2*1800 * 21/121 = 624.79

INSERT INTO sale_items (sale_id, product_id, quantity, unit_price, unit_cost, tax_rate)
VALUES (:sale_id, 2, 2, 1800.00, 1350.00, 21.00);

INSERT INTO payments (sale_id, method, amount) VALUES (:sale_id, 'TRANSFER', 3600.00);

INSERT INTO stock_movements (product_id, store_id, batch_id, movement_type, quantity,
                             unit_cost, sale_id, performed_by)
VALUES (2, 1, NULL, 'SALE', -2, 1350.00, :sale_id, 2);

UPDATE stock_balances SET quantity = quantity - 2 WHERE product_id = 2 AND store_id = 1;  -- 18 -> 16
COMMIT;
```

**What you should see:** P2 balance **16**; two `COMPLETED` sales exist (numbers 1 and 2) in session 1.

#### 5.2.3 FIFO spanning — one line drawn from two batches (rolled back)

P3 = 6.000 (L-2475: 1.000, L-2601: 5.000). Sell 2.500 kg: 1.000 must come from the older lot (which is then fully consumed) and 1.500 from the newer one (**P-04**). Everything rolls back — this is a pure demonstration.

```sql
BEGIN;
INSERT INTO sales (cashier_session_id, sale_number, status, subtotal, tax_amount, discount_amount, total)
VALUES (1, 3, 'COMPLETED', 16250.00, 2820.25, 0.00, 16250.00)
RETURNING id AS sale_id \gset
-- 2.500 kg * 6500.00 = 16250.00 ; tax = 16250 * 21/121 = 2820.25

INSERT INTO sale_items (sale_id, product_id, quantity, unit_price, unit_cost, tax_rate)
VALUES (:sale_id, 3, 2.500, 6500.00, 4320.00, 21.00);
-- line unit_cost = weighted allocation cost (1.000*4500 + 1.500*4200)/2.500 = 4320.00;
-- the authoritative per-batch costs live in sale_item_batches

INSERT INTO sale_item_batches (sale_item_id, batch_id, quantity, unit_cost)
SELECT id, 2, 1.000, 4500.00 FROM sale_items WHERE sale_id = :sale_id;   -- 1.000 from L-2475 (older)
INSERT INTO sale_item_batches (sale_item_id, batch_id, quantity, unit_cost)
SELECT id, 1, 1.500, 4200.00 FROM sale_items WHERE sale_id = :sale_id;   -- 1.500 from L-2601

UPDATE product_batches SET remaining_quantity = remaining_quantity - 1.000,
                           is_consumed = (remaining_quantity - 1.000 = 0)
WHERE id = 2;                                                             -- L-2475: 1.000 -> 0, consumed
UPDATE product_batches SET remaining_quantity = remaining_quantity - 1.500
WHERE id = 1;                                                             -- L-2601: 5.000 -> 3.500

INSERT INTO payments (sale_id, method, amount) VALUES (:sale_id, 'CASH', 16250.00);

INSERT INTO stock_movements (product_id, store_id, batch_id, movement_type, quantity,
                             unit_cost, sale_id, performed_by)
VALUES (3, 1, 2, 'SALE', -1.000, 4500.00, :sale_id, 2),
       (3, 1, 1, 'SALE', -1.500, 4200.00, :sale_id, 2);

UPDATE stock_balances SET quantity = quantity - 2.500 WHERE product_id = 3 AND store_id = 1;

-- VERIFY the span BEFORE rolling back
SELECT si.id, p.name, si.quantity,
       COUNT(sib.id) AS batches_used,
       SUM(sib.quantity) AS allocated
FROM sale_items si
JOIN sale_item_batches sib ON sib.sale_item_id = si.id
JOIN products p ON p.id = si.product_id
WHERE si.sale_id = :sale_id
GROUP BY si.id, p.name, si.quantity;
-- EXPECTED: one sale_item, quantity 2.500, batches_used = 2, allocated = 2.500

SELECT id, batch_number, remaining_quantity, is_consumed FROM product_batches WHERE product_id = 3 ORDER BY id;
-- EXPECTED: L-2475 0.000 / TRUE ; L-2601 3.500 / FALSE

SELECT id, batch_id, quantity FROM stock_movements WHERE sale_id = :sale_id;
-- EXPECTED: two SALE rows: -1.000 batch 2, -1.500 batch 1
ROLLBACK;
```

**What you should see (after rollback):** P3 back to **6.000**, L-2475 back to **1.000**, no trace of the demo (BR-04 semantics hold on rollback).

#### 5.2.4 Negative stock is rejected (BR-04 rollback proof)

P1 has 7. Try to sell 10:

```sql
BEGIN;
INSERT INTO sales (cashier_session_id, sale_number, status, subtotal, tax_amount, discount_amount, total)
VALUES (1, 4, 'COMPLETED', 15000.00, 2603.31, 0.00, 15000.00)
RETURNING id AS sale_id \gset
INSERT INTO sale_items (sale_id, product_id, quantity, unit_price, unit_cost, tax_rate)
VALUES (:sale_id, 1, 10, 1500.00, 900.00, 21.00);
INSERT INTO payments (sale_id, method, amount) VALUES (:sale_id, 'CASH', 15000.00);
INSERT INTO stock_movements (product_id, store_id, batch_id, movement_type, quantity,
                             unit_cost, sale_id, performed_by)
VALUES (1, 1, NULL, 'SALE', -10, 900.00, :sale_id, 2);
UPDATE stock_balances SET quantity = quantity - 10 WHERE product_id = 1 AND store_id = 1;
-- EXPECTED: ERROR — violates the check constraint on stock_balances.quantity (< 0)
ROLLBACK;
```

**What you should see:**
- The `UPDATE` fails; the whole transaction is rolled back — **nothing is half-written** (**BR-04**).
- After rollback: P1 still **7**; the failed sale and its movement are **not** in the tables.

```sql
SELECT COUNT(*) FROM sales WHERE cashier_session_id = 1;          -- EXPECTED: 2 (only sales A and B)
SELECT quantity FROM stock_balances WHERE product_id = 1;         -- EXPECTED: 7
```

#### 5.2.5 Concurrency — two registers, same product (optional, terminal)

> **Stateful:** this leaves a committed sale. Run it **last**, on a fresh reset (§2 + §4), or re-reset before continuing to §5.4.

**Tab A (holds the lock, commits a 1-unit sale of P1):**
```sql
BEGIN;
SELECT id FROM stock_balances WHERE store_id = 1 AND product_id = 1 FOR UPDATE;   -- acquires the row lock
SELECT pg_sleep(10);                                                                -- hold it while Tab B tries
INSERT INTO sales (cashier_session_id, sale_number, status, subtotal, tax_amount, discount_amount, total)
VALUES (1, 5, 'COMPLETED', 1500.00, 260.33, 0.00, 1500.00)
RETURNING id AS sale_id \gset
INSERT INTO sale_items (sale_id, product_id, quantity, unit_price, unit_cost, tax_rate)
VALUES (:sale_id, 1, 1, 1500.00, 900.00, 21.00);
INSERT INTO payments (sale_id, method, amount) VALUES (:sale_id, 'CASH', 1500.00);
INSERT INTO stock_movements (product_id, store_id, batch_id, movement_type, quantity, unit_cost, sale_id, performed_by)
VALUES (1, 1, NULL, 'SALE', -1, 900.00, :sale_id, 2);
UPDATE stock_balances SET quantity = quantity - 1 WHERE product_id = 1 AND store_id = 1;  -- 8 -> 7
COMMIT;
```

**Tab B (blocks, re-reads, cannot overdraw):**
```sql
BEGIN;
SELECT id FROM stock_balances WHERE store_id = 1 AND product_id = 1 FOR UPDATE;
-- EXPECTED: this SELECT BLOCKS until Tab A commits (~10 s), then returns the fresh row
UPDATE stock_balances SET quantity = quantity - 10 WHERE product_id = 1 AND store_id = 1;
-- EXPECTED: ERROR — negative stock is structurally impossible (03 §6)
ROLLBACK;
```

**What you should see:** the second transaction waits on the row lock, reads the **post-commit** balance, and cannot push it negative. This is the two-register race from 03 F-07 Edge cases, proven at the engine level.

### 5.3 F-08: Full Void

Voids **sale B** (id returned in §5.2.2 — confirm below) and restores everything.

```sql
SELECT id FROM sales WHERE cashier_session_id = 1 AND sale_number = 2;  -- note the id (expected 2)
```

```sql
BEGIN;
-- Guard: only a COMPLETED sale can be voided (prevents double-void)
SELECT * FROM sales WHERE id = 2 AND status = 'COMPLETED' FOR UPDATE;

UPDATE sales SET status = 'VOIDED' WHERE id = 2;

-- Restore stock: RETURN movement per allocation (P-01 / BR-05)
INSERT INTO stock_movements (product_id, store_id, batch_id, movement_type, quantity,
                             unit_cost, sale_id, performed_by, notes)
VALUES (2, 1, NULL, 'RETURN', 2, 1350.00, 2, 1, 'anulacion venta nro 2');

UPDATE stock_balances SET quantity = quantity + 2 WHERE product_id = 2 AND store_id = 1;  -- 16 -> 18

-- Audit (NFR-03)
INSERT INTO audit_log (user_id, table_name, action, record_id, old_data, new_data)
VALUES (1, 'sales', 'VOID', 2, '{"status":"COMPLETED"}', '{"status":"VOIDED"}');
COMMIT;
```

**What you should see:**
- Sale 2 status = `VOIDED`; P2 back to **18**; a `RETURN` movement (+2) balances the original `SALE` (−2).
- A `VOID` entry exists in `audit_log`.

```sql
SELECT status FROM sales WHERE id = 2;   -- EXPECTED: VOIDED
SELECT quantity FROM stock_balances WHERE product_id = 2 AND store_id = 1;   -- EXPECTED: 18
```

**Double-void is rejected:**
```sql
BEGIN;
UPDATE sales SET status = 'VOIDED' WHERE id = 2 AND status = 'COMPLETED';
-- EXPECTED: UPDATE 0 (already VOIDED — no-op, no double restoration)
ROLLBACK;
```

### 5.4 F-09: Stock Count & Merma

State entering this section: P1 = 7, P2 = 18, P3 = 6.000. Physical story: the clerk logs a known family consumption (P1 −1), then counts — P1 comes up 5 (one more unit unaccounted = merma), P2 matches at 18.

```sql
-- Open the count (DRAFT) and snapshot system quantities
INSERT INTO stock_counts (store_id, status, created_by)
VALUES (1, 'DRAFT', 3) RETURNING id AS count_id \gset

INSERT INTO stock_count_items (count_id, product_id, system_quantity, counted_quantity)
SELECT :count_id, product_id, quantity, quantity
FROM stock_balances WHERE store_id = 1 AND product_id IN (1, 2);

-- Known exit first (paper log): family consumption -> ADJUSTMENT / OWN_CONSUMPTION
INSERT INTO stock_movements (product_id, store_id, batch_id, movement_type, reason, quantity,
                             unit_cost, count_id, performed_by, notes)
VALUES (1, 1, NULL, 'ADJUSTMENT', 'OWN_CONSUMPTION', -1, 900.00, :count_id, 3, 'consumo familiar anotado en papel');
UPDATE stock_balances SET quantity = quantity - 1 WHERE product_id = 1 AND store_id = 1;  -- 7 -> 6

-- Refresh the item snapshot to the live balance (convention below), then record the physical count
UPDATE stock_count_items SET system_quantity = 6 WHERE count_id = :count_id AND product_id = 1;
UPDATE stock_count_items SET counted_quantity = 5 WHERE count_id = :count_id AND product_id = 1;
-- P2 stays counted = system = 18

-- Confirm (atomic; one-way DRAFT -> CONFIRMED)
BEGIN;
UPDATE stock_counts SET status = 'CONFIRMED', confirmed_at = now()
WHERE id = :count_id AND status = 'DRAFT';

INSERT INTO stock_movements (product_id, store_id, batch_id, movement_type, reason, quantity,
                             unit_cost, count_id, performed_by, notes)
SELECT product_id, 1, NULL, 'ADJUSTMENT', 'COUNT', counted_quantity - system_quantity,
       900.00, :count_id, 3, 'diferencia de conteo'
FROM stock_count_items
WHERE count_id = :count_id AND counted_quantity <> system_quantity;

UPDATE stock_balances b SET quantity = sci.counted_quantity
FROM stock_count_items sci
WHERE sci.count_id = :count_id AND sci.product_id = b.product_id AND b.store_id = 1
  AND sci.counted_quantity <> sci.system_quantity;
COMMIT;
```

> **Spec note (known exits vs. counted products):** 03 F-09 computes the confirm adjustment as `counted_quantity − system_quantity` using the item's **snapshot**. If a known-exit `ADJUSTMENT` is posted against a product that is also counted and the snapshot is not refreshed, the exit is counted twice and `ledger == balance` breaks (§6.1). This manual adopts the convention that the item snapshot is refreshed to the live balance when a known exit is logged. **Flag this for a v1.1 spec decision**: either refresh the snapshot (as here) or compute the diff against the live balance at confirm time.

**What you should see:**
- P1 balance = **5** (8 − 1 sale − 1 consumption − 1 count merma); P2 untouched at **18**; P3 untouched at **6.000**.
- Exactly two new `ADJUSTMENT` movements: `OWN_CONSUMPTION` −1 and `COUNT` −1 — the report layer can separate "explained loss" from "true merma" (03 F-09 invariants).
- Confirming twice is a no-op:

```sql
SELECT quantity FROM stock_balances WHERE product_id = 1 AND store_id = 1;   -- EXPECTED: 5
SELECT movement_type, reason, quantity FROM stock_movements
WHERE count_id = :count_id ORDER BY id;
-- EXPECTED: ADJUSTMENT / OWN_CONSUMPTION / -1 ; ADJUSTMENT / COUNT / -1

BEGIN;
UPDATE stock_counts SET status = 'CONFIRMED' WHERE id = :count_id AND status = 'DRAFT';
-- EXPECTED: UPDATE 0 (already CONFIRMED — one-way transition)
ROLLBACK;
```

### 5.5 F-12: Session Close / Cash Audit

Expected cash = opening float + CASH from `COMPLETED` sales. The only completed sale is sale A (CASH 8,000); the voided sale B is **excluded** (03 F-12 invariant). Cashier declares 57,900 → shortage 100 → variance recorded and audited.

```sql
-- Expected cash for the CASH method (voided sales excluded by the status filter)
SELECT cs.opening_amount
     + COALESCE((SELECT SUM(pm.amount) FROM payments pm
                 JOIN sales s ON s.id = pm.sale_id
                 WHERE s.cashier_session_id = cs.id AND s.status = 'COMPLETED' AND pm.method = 'CASH'), 0)
       AS expected_cash
FROM cashier_sessions cs WHERE cs.id = 1;
-- EXPECTED: 58000.00  (50000.00 float + 8000.00 from sale A; sale B's 3600 is TRANSFER and VOIDED)

-- Blind close: declared amounts per method
INSERT INTO cash_audits (cashier_session_id, method, declared_amount)
VALUES (1, 'CASH', 57900.00);

-- Variance: declared - expected
SELECT ca.declared_amount - (cs.opening_amount + 8000.00) AS cash_variance
FROM cash_audits ca JOIN cashier_sessions cs ON cs.id = ca.cashier_session_id
WHERE ca.cashier_session_id = 1 AND ca.method = 'CASH';
-- EXPECTED: -100.00  (shortage; negative = faltante, never silently ignored)

-- Audit the discrepancy (NFR-03) and close
INSERT INTO audit_log (user_id, table_name, action, record_id, old_data, new_data)
VALUES (2, 'cashier_sessions', 'CLOSE', 1,
        '{"status":"OPEN","expected_cash":58000.00}',
        '{"status":"CLOSED","declared_cash":57900.00,"variance":-100.00}');

UPDATE cashier_sessions SET status = 'CLOSED', closing_amount = 57900.00, closed_at = now()
WHERE id = 1 AND status = 'OPEN';
```

**What you should see:**
- Session 1 `CLOSED`, `closing_amount` = 57900.00; `audit_log` holds the discrepancy entry (**NFR-03**).
- After close, BR-02 blocks new sales — the F-07 session guard returns nothing:

```sql
SELECT id FROM cashier_sessions WHERE id = 1 AND status = 'OPEN' FOR UPDATE;
-- EXPECTED: 0 rows (session no longer OPEN)
```

---

## 6. Invariant Queries

These are the integrity checks every test run must pass. **Any mismatch here is a blocking defect.**

### 6.1 Ledger == balance (per product)

```sql
SELECT b.product_id, p.name, b.quantity AS balance,
       (SELECT COALESCE(SUM(m.quantity), 0) FROM stock_movements m WHERE m.product_id = b.product_id) AS ledger
FROM stock_balances b JOIN products p ON p.id = b.product_id
WHERE b.store_id = 1
ORDER BY b.product_id;
```

- P1: 8 −1 (sale) −1 (consumption) −1 (count) = **5** = 5
- P2: 12 +6 (reception) −2 (sale) +2 (void) = **18** = 18
- P3: 5 +2 −1 = **6.000** = 6.000
- EXPECTED: `balance = ledger` for all three rows (BR-05 / 03 §6).

### 6.2 Payments == total (per sale, voided included)

```sql
SELECT s.id, s.status, s.total, SUM(pm.amount) AS paid
FROM sales s JOIN payments pm ON pm.sale_id = s.id
WHERE s.cashier_session_id = 1
GROUP BY s.id, s.status, s.total;
-- EXPECTED: sale 1 (COMPLETED) total 8000.00 = paid 8000.00 ; sale 2 (VOIDED) total 3600.00 = paid 3600.00
```

### 6.3 No negative stock anywhere

```sql
SELECT COUNT(*) FROM stock_balances WHERE quantity < 0;
-- EXPECTED: 0
```

### 6.4 FIFO allocation ↔ ledger (same batch_id per allocation)

```sql
SELECT sib.sale_item_id, sib.batch_id, sib.quantity AS allocated,
       (SELECT COALESCE(SUM(-1 * m.quantity), 0) FROM stock_movements m
         WHERE m.sale_id = si.sale_id AND m.batch_id = sib.batch_id) AS sold_from_batch
FROM sale_item_batches sib
JOIN sale_items si ON si.id = sib.sale_item_id
WHERE si.sale_id = 1;
-- EXPECTED: batch 2 allocated 1.000, sold_from_batch 1.000 (the SALE row is -1.000)
-- (P1's line of sale 1 has no sale_item_batches row — batchless, and no SALE movement needs one)
```

---

## 7. Guard Tests (defense-in-depth)

Run each failing statement, confirm the error, then `ROLLBACK` where a `BEGIN` was opened.

### 7.1 Append-only ledger (BR-05 trigger from 03 §6)

```sql
UPDATE stock_movements SET quantity = quantity + 1 WHERE id = 1;
-- EXPECTED: ERROR — the trigger rejects UPDATE on the ledger

DELETE FROM stock_movements WHERE id = 1;
-- EXPECTED: ERROR — the trigger rejects DELETE on the ledger
```

### 7.2 FK protection (NFR-02)

```sql
DELETE FROM products WHERE id = 1;
-- EXPECTED: ERROR — referenced by sale_items / stock_movements / prices / ...
ROLLBACK;

DELETE FROM suppliers WHERE id = 1;
-- EXPECTED: ERROR — referenced by purchases
ROLLBACK;
```

### 7.3 Ledger source reference (exactly one of sale / purchase / count)

```sql
BEGIN;
INSERT INTO stock_movements (product_id, store_id, movement_type, quantity, unit_cost, performed_by)
VALUES (1, 1, 'ADJUSTMENT', -1, 900.00, 1);
-- EXPECTED: ERROR — CHECK (num_nonnulls(sale_id, purchase_id, count_id) = 1)
ROLLBACK;
```

### 7.4 Sign per movement type

```sql
BEGIN;
INSERT INTO stock_movements (product_id, store_id, movement_type, quantity, unit_cost, sale_id, performed_by)
VALUES (1, 1, 'SALE', 1, 900.00, 1, 1);
-- EXPECTED: ERROR — SALE must be negative
ROLLBACK;
```

### 7.5 Unique constraints

```sql
BEGIN;
INSERT INTO products (sku, name, category_id, measure_unit, sale_type)
VALUES ('ORE-200G', 'Duplicate SKU', 2, 'UNIT', 'UNIT');
-- EXPECTED: ERROR — UNIQUE(sku)
ROLLBACK;

BEGIN;
INSERT INTO prices (product_id, unit_price, valid_from)
VALUES (1, 1600.00, '2026-01-01T00:00:00Z');   -- same valid_from as the seed price
-- EXPECTED: ERROR — UNIQUE(product_id, valid_from), no overlapping vigencias (F-03)
ROLLBACK;
```

---

## 8. Report Smoke Tests

Run each report against the current state and compare with the expected figures (derived from seed + §5). Reports are **queries only** — nothing is stored (03 §5).

### R-01: Replenishment

```sql
SELECT p.name, b.quantity, p.reorder_point, p.max_stock, s.name AS preferred_supplier
FROM products p
JOIN stock_balances b ON b.product_id = p.id AND b.store_id = 1
LEFT JOIN suppliers s ON s.id = p.preferred_supplier_id
WHERE p.is_active AND b.quantity <= p.reorder_point
ORDER BY b.quantity / NULLIF(p.reorder_point, 0) ASC, p.name;
-- EXPECTED: only "Galletitas Oreo 200g" (5 <= reorder 10; max_stock 50; Distribuidora del Sur S.A.)
```

### R-02: Expiring Soon

```sql
SELECT p.name, pb.batch_number, pb.expiry_date, pb.remaining_quantity,
       (pb.remaining_quantity * pb.unit_cost) AS value_at_cost
FROM product_batches pb
JOIN products p ON p.id = pb.product_id
JOIN app_settings a ON a.key = 'expiry_warning_days'
WHERE pb.expiry_date <= CURRENT_DATE + (a.value #>> '{}')::int
  AND pb.remaining_quantity > 0;
-- EXPECTED: one row — Mortadela / L-2475 / 2026-08-20 / 1.000 / 4500.00
-- (L-2601 expires 2026-12-15, outside the 30-day window)
```

### R-03: Top Products / ABC

```sql
SELECT p.id, p.name, SUM(si.quantity) AS units,
       SUM(si.quantity * si.unit_price) AS revenue,
       SUM((si.unit_price - si.unit_cost) * si.quantity) AS margin
FROM sale_items si
JOIN sales s ON s.id = si.sale_id
JOIN products p ON p.id = si.product_id
WHERE s.status = 'COMPLETED' AND s.created_at >= CURRENT_DATE
GROUP BY p.id, p.name
ORDER BY revenue DESC;
-- EXPECTED (revenue 8000, only the completed sale):
--   Mortadela           1.000 / 6500.00 / 2000.00   (A: 81%)
--   Galletitas Oreo 200g 1     / 1500.00 /  600.00   (B)
-- Sale B (P2) is absent: VOIDED sales are excluded.
```

### R-04: Slow Movers ("hueso")

```sql
SELECT p.name, b.quantity, p.reorder_point
FROM products p
JOIN stock_balances b ON b.product_id = p.id AND b.store_id = 1
WHERE p.is_active AND b.quantity > p.reorder_point
  AND NOT EXISTS (
        SELECT 1 FROM sale_items si
        JOIN sales s ON s.id = si.sale_id
        WHERE si.product_id = p.id AND s.status = 'COMPLETED' AND s.created_at >= CURRENT_DATE)
ORDER BY p.name;
-- EXPECTED: only "Soda Cola 1.5L" (18 on hand, zero completed sales in the period)
```

### R-05: Inventory Valuation

```sql
SELECT c.category_id, cat.name, SUM(b.quantity * c.unit_cost) AS value
FROM stock_balances b
JOIN (SELECT DISTINCT ON (product_id) product_id, unit_cost
      FROM product_costs ORDER BY product_id, valid_from DESC) c ON c.product_id = b.product_id
JOIN categories cat ON cat.id = c.category_id
WHERE b.store_id = 1
GROUP BY c.category_id, cat.name;
-- EXPECTED: Almacen = 28800.00 (P1 5*900 + P2 18*1350) ; Fiambreria = 27000.00 (P3 6*4500)
-- total on hand = 55800.00
```

### R-06: Sales by Period & Payment Method

```sql
SELECT s.created_at::date AS day, pm.method, COUNT(DISTINCT s.id) AS sales,
       SUM(pm.amount) AS revenue,
       SUM(si.quantity) AS items
FROM sales s
JOIN payments pm ON pm.sale_id = s.id
JOIN sale_items si ON si.sale_id = s.id
WHERE s.status = 'COMPLETED' AND s.created_at >= CURRENT_DATE
GROUP BY day, pm.method;
-- EXPECTED: today / CASH / 1 sale / 8000.00 / 2 items
-- (the payment-method split of the voided sale is absent)
```

### R-07: Cash & Margin Report

```sql
WITH session_expected AS (
    SELECT cs.id, cs.opening_amount,
           cs.opening_amount
             + COALESCE(SUM(pm.amount) FILTER (WHERE pm.method = 'CASH' AND s.status = 'COMPLETED'), 0)
               AS expected_cash,
           COALESCE(SUM(pm.amount) FILTER (WHERE s.status = 'COMPLETED'), 0) AS sales_total
    FROM cashier_sessions cs
    LEFT JOIN sales s ON s.cashier_session_id = cs.id
    LEFT JOIN payments pm ON pm.sale_id = s.id
    WHERE cs.id = 1
    GROUP BY cs.id, cs.opening_amount
),
margin AS (
    SELECT SUM((si.unit_price - si.unit_cost) * si.quantity) AS gross_margin
    FROM sale_items si
    JOIN sales s ON s.id = si.sale_id
    WHERE s.cashier_session_id = 1 AND s.status = 'COMPLETED'
)
SELECT se.id, se.opening_amount, se.sales_total, se.expected_cash,
       ca.declared_amount,
       ca.declared_amount - se.expected_cash AS cash_variance,
       m.gross_margin
FROM session_expected se
LEFT JOIN cash_audits ca ON ca.cashier_session_id = se.id AND ca.method = 'CASH'
CROSS JOIN margin m;
-- EXPECTED: opening 50000.00 / sales 8000.00 / expected cash 58000.00 /
--           declared 57900.00 / variance -100.00 / gross margin 2600.00
-- margin = (1500.00-900.00)*1 + (6500.00-4500.00)*1.000 = 2600.00 (exact batch costs, P-04)
```

---

## 9. Result Checklist

| # | Test | Location | Pass criteria |
|---|---|---|---|
| 1 | Schema / enums / constraints / indexes | §3 | every row of the table matches |
| 2 | Seed builds consistent initial stock | §4 | P1=8, P2=12, P3=7.000; ledger==balance |
| 3 | F-06 reception + avg cost | §5.1 | P2 18; no batch row; avg 1316.67 |
| 4 | F-07 sale A (snapshots, FIFO, BR-02) | §5.2.1 | snapshots stored; L-2475 → 1.000; P1 7, P3 6.000 |
| 5 | F-07 sale B | §5.2.2 | P2 16; payments==total |
| 6 | F-07 FIFO spanning (P-04) | §5.2.3 | one line, two batches, is_consumed flips; rolled back cleanly |
| 7 | F-07 negative stock | §5.2.4 | ERROR + full rollback, state unchanged |
| 8 | F-07 concurrency (two registers) | §5.2.5 | second tx blocks, cannot overdraw (optional/terminal) |
| 9 | F-08 void + audit | §5.3 | status VOIDED; P2 18; RETURN balances SALE; double-void no-op |
| 10 | F-09 count, known exit, confirm | §5.4 | P1 5; OWN_CONSUMPTION + COUNT adjustments; one-way confirm |
| 11 | F-12 close + variance audit | §5.5 | session CLOSED; expected 58000 vs declared 57900; audit_log entry |
| 12 | Invariants ledger/payments/negative/FIFO↔ledger | §6 | all queries match expected |
| 13 | Guards: trigger, FKs, CHECKs, UNIQUEs | §7 | each error fires as documented |
| 14 | Reports R-01..R-07 | §8 | figures match the expected block |

Every check must be **PASS** before the backend is written. Any failure is a defect in the DDL/migrations (from `02`) or in the transactional spec (`03`) — fix it at the source, re-migrate, and re-run from §1.
