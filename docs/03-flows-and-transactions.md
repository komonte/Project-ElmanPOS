# 03. Flows & Transactions

## 1. Purpose & How to Read This Document

This document specifies the **behavior** of the system: every operation, from the user action on screen down to the atomic database work underneath it. It is the contract the Go backend implements 1:1.

**Two layers per flow:**
1. **Business flow** — who initiates it, what the user does, what they see.
2. **DB transaction** — the exact `BEGIN...COMMIT` sequence, row locks, statements, and rollback behavior.

**Fixed template used for every flow (F-XX):**

```
- Actor / Role:        who initiates it
- Trigger:             the event that starts the flow
- Preconditions:       state that must already be true (with BR/NFR refs)
- Business flow:       ordered on-screen steps
- DB transaction:      BEGIN → statements → locks → COMMIT
- Invariants:          truths that must always hold when the flow ends
- Errors / rollback:   behavior on failure at any step
- Edge cases:          corner situations with a defined resolution
```

Reports (R-XX) are **computed by queries** — nothing is stored. Conventions referenced throughout:
- **Money:** `NUMERIC` only (**BR-01**). Floating point is prohibited.
- **Prices include IVA** (standard Argentine retail convention). `unit_price` on a line is the consumer price; the IVA component is derived per line: `tax = line_total × rate / (100 + rate)`.
- **Ledger:** every stock change is a `stock_movements` row (**BR-05**); `stock_balances` is maintained in the same transaction.

---

## 2. Catalog & Pricing

### F-01: Product Create / Edit

- **Actor / Role:** Admin (`ADMIN`).
- **Trigger:** Admin opens the catalog and creates a new product or edits an existing one.
- **Preconditions:**
  - Authenticated as `ADMIN`.
  - Category exists (`products.category_id` required).
  - If `sale_type = 'COMPOSITE'`, components must already exist (F-05).
- **Business flow:**
  1. Form: `sku`, `name`, `description`, `brand`, `category`, `measure_unit`, `sale_type`, `controls_batches`, `reorder_point`, `max_stock`, `margin_percent`, `preferred_supplier_id`, `is_consignment`, `is_active`.
  2. Save → system validates and persists (create: `INSERT`; edit: `UPDATE`).
- **DB transaction:**
  1. `BEGIN;`
  2. `INSERT INTO products (...)` or `UPDATE products SET ... WHERE id = $1;`
  3. `COMMIT;`
- **Invariants:**
  - `sku` unique (`UNIQUE(sku)`).
  - `max_stock` either `NULL` or strictly greater than `reorder_point` (`CHECK`).
  - Once the product has any `stock_movements` or `sale_items`, the fields `sale_type`, `controls_batches`, and `measure_unit` **cannot be changed** (changing them would corrupt past valuation). Only an `ADMIN` may change them after verifying zero stock, and it must be recorded in `audit_log`.
- **Errors / rollback:** duplicate `sku` → reject with message; any failure → full rollback.
- **Edge cases:**
  - Mass import (SEPA ETL): sets `reorder_point`, `margin_percent`, and category defaults in bulk; individual products are tuned later.
  - Deactivating (`is_active = FALSE`) keeps history intact; product disappears from POS search but remains in reports.

### F-02: Barcode Management

- **Actor / Role:** Admin.
- **Trigger:** Admin adds, edits, or removes a barcode for a product.
- **Preconditions:** Product exists.
- **Business flow:** select product → add `code` + `code_type` (`EAN13`/`EAN8`/`INTERNAL`) → save.
- **DB transaction:** `INSERT` / `DELETE` on `product_codes` (`UNIQUE(code)` enforced at engine level).
- **Invariants:** a code can never belong to two products (uniqueness guarantees the POS scan resolves unambiguously).
- **Errors / rollback:** duplicate or invalid-format code → rejected.
- **Edge cases:**
  - Manufacturer changes packaging → add the new EAN to the **same** product; both codes scan to it.
  - Store-printed codes (`INTERNAL`) for composite packs and scale labels.

### F-03: Price Update

- **Actor / Role:** Admin.
- **Trigger:** Admin changes the sale price (PVP) of a product.
- **Preconditions:** Product exists and is active.
- **Business flow:** select product → enter new `unit_price` + `valid_from` (default: now) → save.
- **DB transaction:**
  1. `INSERT INTO prices (product_id, unit_price, valid_from) ...`
  2. `INSERT INTO audit_log (user_id, 'prices', 'INSERT', ...)` — price changes are audited (**NFR-03**).
- **Invariants:**
  - `UNIQUE(product_id, valid_from)` — no overlapping vigencias.
  - Only one effective price at any moment.
  - Past `sale_items` are never retroactively altered (**BR-03**).
- **Errors / rollback:** `valid_from` overlapping an existing period → rejected (or the existing period must be closed first).
- **Edge cases:** cost-driven suggestion flow is F-04; bulk price updates by category are an extension (deferred).

### F-04: Cost Change → Average Cost & PVP Suggestion

- **Actor / Role:** Admin, or triggered automatically by F-06 (purchase reception).
- **Trigger:** A new `unit_cost` is recorded (from a purchase or a manual entry).
- **Preconditions:** Product exists.
- **Business flow:**
  1. New cost arrives.
  2. System inserts a `product_costs` row (`unit_cost`, recalculated `avg_cost`).
  3. If `margin_percent > 0`, the system **suggests** `pvp_suggested = unit_cost / (1 - margin_percent/100)`; admin accepts or overrides it (which runs F-03).
- **DB transaction:**
  1. `INSERT INTO product_costs (product_id, unit_cost, avg_cost, valid_from) ...`
  2. `INSERT INTO audit_log (...)`.
- **Weighted average cost formula** (running average on each reception):
  ```
  new_avg = (prev_avg × prev_qty_on_hand + new_cost × new_qty) / (prev_qty_on_hand + new_qty)
  ```
  where `prev_qty_on_hand` is the current `stock_balances.quantity` at reception time.
- **Invariants:** `avg_cost` history is append-only; past margins are protected by line-item snapshots (**BR-03**).
- **Errors / rollback:** a failed insertion rolls back the cost row; the suggestion is never persisted until the admin confirms the price change.
- **Edge cases:** `margin_percent = 0` → no suggestion (manual pricing only). Cost decreases → suggestion drops; admin decides.

### F-05: Composite Pack Management

- **Actor / Role:** Admin.
- **Trigger:** Admin defines a seasonal kit (e.g. candy box) or edits an existing one.
- **Preconditions:** Component products exist and are `UNIT`/`WEIGHT` (never `COMPOSITE` — nesting is forbidden in v1).
- **Business flow:** create a product with `sale_type = 'COMPOSITE'` → add components with quantities (`product_components`) → assign an `INTERNAL` barcode for printing.
- **DB transaction:**
  1. `INSERT INTO products (... sale_type='COMPOSITE')`
  2. `INSERT INTO product_components` (one per component)
  3. `INSERT INTO product_codes (code_type='INTERNAL')`
- **Invariants:** `pack_id <> component_id` (`CHECK`); components must be active.
- **Errors / rollback:** component inactive or nested → rejected.
- **Edge cases:** changing a pack recipe only affects future sales (components are dereferenced at sale time, not stored in the sale line).

---

## 3. Stock & Sales

### F-06: Purchase Reception

- **Actor / Role:** Stock Clerk (`STOCK_CLERK`) or Admin.
- **Trigger:** Goods arrive with a supplier invoice / remito.
- **Preconditions:** Supplier exists; each product exists and is active.
- **Business flow:**
  1. Load supplier, voucher number, totals.
  2. Add lines: product, `quantity`, `unit_cost`.
  3. For products with `controls_batches = TRUE`: enter `batch_number` and `expiry_date` per line.
  4. Confirm reception.
- **DB transaction:**
  1. `BEGIN;`
  2. `INSERT INTO purchases (... status='RECEIVED')`
  3. `INSERT INTO purchase_items` (one per line)
  4. For each line with `controls_batches`: `INSERT INTO product_batches (original_quantity = qty, remaining_quantity = qty, unit_cost, expiry_date)`; set `purchase_items.batch_id`. Batches with the **same `batch_number` + `expiry_date` merge into one row** (quantities summed).
  5. `INSERT INTO stock_movements` (type `PURCHASE`, positive, per batch, `purchase_id` reference).
  6. `UPDATE stock_balances SET quantity = quantity + qty;`
  7. `INSERT INTO product_costs` (new `unit_cost` + recalculated `avg_cost` per F-04 formula).
  8. `COMMIT;`
- **Invariants:** for every received unit, ledger, balance, and batch `remaining_quantity` all increase by the same amount; `avg_cost` is monotonic-correct.
- **Errors / rollback:** any failure → full rollback (no partial reception). Product missing from catalog → block reception until created.
- **Edge cases:**
  - Non-batch products (soda): no `product_batches` row, no batch friction at reception.
  - Same lot received across two vouchers: they are separate `product_batches` rows unless the clerk consolidates the batch entry (merge only applies within the same reception line group).
  - Consignment reception: **deferred** (§7).

### F-07: Atomic Sale (Core Flow)

- **Actor / Role:** Cashier (`CASHIER`).
- **Trigger:** Barcode scanned or product searched while a sale is in progress.
- **Preconditions:**
  - An `OPEN` cashier session exists for the current register (**BR-02**).
  - Product `is_active = TRUE` and has a current price in `prices`.
- **Business flow:**
  1. Scan / search → resolve `product_codes.code` → product (target **NFR-01**, <100 ms).
  2. If `sale_type = 'WEIGHT'` → weight mode (F-10); else default `quantity = 1`.
  3. Line added with `unit_price` snapshot from current `prices`.
  4. Repeat until cart complete; quantities adjustable; lines removable.
  5. Cashier selects payment method(s); for `CASH`, change is computed from the tendered amount.
  6. Confirm → system validates totals and processes atomically.
  7. On success: receipt prints; cart resets.
- **DB transaction (single `BEGIN...COMMIT`):**
  1. `BEGIN;`
  2. `SELECT id FROM cashier_sessions WHERE id = $1 AND status='OPEN' FOR UPDATE;` (**BR-02** guard).
  3. `SELECT ... FROM stock_balances WHERE product_id IN (...) AND store_id = $1 FOR UPDATE;` — lock every touched product, in **ascending `product_id` order** to avoid deadlocks (§6).
  4. For each line, FIFO-allocate: pick `product_batches` with `remaining_quantity > 0` ordered by `(expiry_date, id)`; if the line spans batches, create one allocation per batch.
     - `INSERT INTO sale_item_batches (sale_item_id, batch_id, quantity, unit_cost);`
     - `UPDATE product_batches SET remaining_quantity = remaining_quantity - q, is_consumed = (remaining_quantity - q = 0);`
  5. `INSERT INTO sale_items (product_id, quantity, unit_price, unit_cost, tax_rate)` — price/cost **snapshots** (**BR-03**). For `COMPOSITE` products, instead of one line, expand components: one `sale_item` per component using each component's price and batch allocation.
  6. `INSERT INTO payments` (one per method; `SUM(payments.amount) = sales.total` validated).
  7. `INSERT INTO sales (header, subtotal, tax_amount, discount_amount, total, status='COMPLETED');`
  8. `INSERT INTO stock_movements` (one per batch allocation, type `SALE`, negative, `batch_id` + `sale_id`).
  9. `UPDATE stock_balances SET quantity = quantity - allocated;`
  10. `COMMIT;`
- **Totals convention:** `unit_price` includes IVA. `subtotal = Σ quantity × unit_price`; `tax_amount = Σ line_total × rate/(100+rate)`; `total = subtotal − discount_amount`.
- **Invariants:**
  - `stock_balances.quantity >= 0` for every touched product after commit (never negative).
  - `SUM(payments.amount) = sales.total`.
  - Ledger equals balance: `Σ stock_movements.quantity(product) = stock_balances.quantity(product)`.
  - Every `sale_item_batches` row has a matching `stock_movements` row with the same `batch_id`.
- **Errors / rollback:**
  - Insufficient stock → `ROLLBACK`, nothing persisted, POS shows "insufficient stock".
  - Payment rejected / validation failed → `ROLLBACK`; cart retained on screen for retry.
  - Any DB error → `ROLLBACK`; caller sees a generic error; nothing half-written (**BR-04**).
- **Edge cases:**
  - **Two registers, same product, simultaneously:** the second blocks on the `FOR UPDATE` row lock, re-reads stock after the first commits; if insufficient → sale rejected. Negative stock is structurally impossible.
  - Repeated scan of the same barcode → increments quantity on the existing line.
  - Split payment (AR$2,000 cash + AR$3,000 transfer) → two `payments` rows.
  - Product priced but inactive → blocked with a clear message.
  - `WEIGHT` line (0.300 kg): FIFO allocates fractional quantities from batches.
  - Undoing a completed sale is **only** possible via F-08 (full void).

### F-08: Full Void

- **Actor / Role:** Admin only (voids are sensitive; **NFR-03**).
- **Trigger:** A completed sale must be entirely undone (wrong charge, customer never picked up, etc.).
- **Preconditions:** Sale `status = 'COMPLETED'`; a `reason` is provided.
- **Business flow:** Admin opens the sale → "Void" → confirms → enters reason → system restores everything.
- **DB transaction:**
  1. `BEGIN;`
  2. `SELECT ... FROM sales WHERE id = $1 AND status = 'COMPLETED' FOR UPDATE;`
  3. `UPDATE sales SET status = 'VOIDED';`
  4. For each `sale_item` allocation: `INSERT INTO stock_movements (type 'RETURN', positive, same batch_id, sale_id reference)`; `UPDATE product_batches SET remaining_quantity = remaining_quantity + q, is_consumed = FALSE WHERE is_consumed = TRUE;`
  5. `UPDATE stock_balances SET quantity = quantity + q;` per restored product.
  6. `INSERT INTO audit_log (user_id, 'sales', 'VOID', ...)`.
  7. `COMMIT;`
- **Invariants:**
  - Voided sales are **excluded** from cash-audit expected amounts (F-12).
  - Stock is restored to the exact original batches; `is_consumed` is cleared if the batch has stock again.
  - Ledger remains consistent (RETURN movements balance the original SALE movements).
- **Errors / rollback:** sale already `VOIDED` → rejected (no double void); any failure → full rollback.
- **Edge cases:**
  - Void after a stock count (F-09) confirmed → stock is restored and the next count picks it up; no special handling.
  - Partial returns (refund a single line) → **deferred** (§7). v1 is all-or-nothing void.

### F-09: Stock Count & Merma

- **Actor / Role:** Stock Clerk counts; Admin confirms. Both record known exits.
- **Trigger:** Periodic physical inventory (weekly / monthly), full or by category.
- **Preconditions:** No restriction on concurrent sales (count items record their `system_quantity` snapshot when added).
- **Business flow:**
  1. **Paper capture (offline):** known exits — family consumption, breakage, spoilage — noted as they happen (low-friction).
  2. Open a `stock_counts` in `DRAFT`; add items (system auto-snaps `system_quantity` from `stock_balances`) and record `counted_quantity`.
  3. **Known exits first:** enter each paper-log entry as an `ADJUSTMENT` movement with `reason = 'OWN_CONSUMPTION' | 'DAMAGE' | 'EXPIRATION'` (optional `batch_id`). The system now reflects explained losses.
  4. **Confirm:** `status = 'CONFIRMED'`.
- **DB transaction (confirm):**
  1. `BEGIN;`
  2. `UPDATE stock_counts SET status='CONFIRMED', confirmed_at=now() WHERE id = $1 AND status='DRAFT';`
  3. For each item where `counted_quantity <> system_quantity`: `INSERT INTO stock_movements (type 'ADJUSTMENT', reason 'COUNT', quantity = counted − system [signed], count_id)`; `UPDATE stock_balances SET quantity = counted_quantity;`
  4. `COMMIT;`
- **Invariants:**
  - After confirm: `stock_balances.quantity = counted_quantity` for every counted product.
  - A count can only be confirmed once (`DRAFT → CONFIRMED` is one-way).
  - The difference between system and counted, minus known exits, is the **true merma** — reports separate `OWN_CONSUMPTION`/`DAMAGE`/`EXPIRATION` from unexplained `COUNT`/`THEFT` losses.
- **Errors / rollback:** confirming a count with pending conflicts → rollback; re-count in DRAFT before confirming.
- **Edge cases:**
  - **Positive difference** (found more than the system says) → positive `ADJUSTMENT`; often a missing `PURCHASE` entry.
  - Product counted physically but absent from system → create it first (F-01), then count.
  - Batch-level count: if the physical count of a `controls_batches` product differs, the adjustment is FIFO-allocated to batches (or `batch_id` is specified manually when the clerk knows which lot is short).

### F-10: WEIGHT Sale Mode (a granel)

- **Actor / Role:** Cashier.
- **Trigger:** A `sale_type = 'WEIGHT'` product is scanned or selected (mortadella, bakery bread).
- **Preconditions:** Product has a current **price per kg** in `prices`.
- **Business flow:**
  1. Select product → screen switches to weight input.
  2. Cashier enters grams/kg (0.300 = 300 g).
  3. Line `quantity = 0.300`, `unit_price` = price per kg, `total = 0.300 × price_per_kg`.
  4. Continue with F-07 (atomic processing).
- **DB transaction:** identical to F-07; `quantity` is `NUMERIC(12,3)`.
- **Invariants:** `quantity > 0`; unit_price used is the per-kg price (never the per-unit one).
- **Errors / rollback:** `0` / negative weight rejected; absurd weights flagged (configurable sanity ceiling in `app_settings`).
- **Edge cases:** Phase 2 — scale-printed GS1 variable-measure barcode (prefix `2`) decoded into weight/price on scan; v1 is manual entry only.

---

## 4. Cash Register

### F-11: Session Open

- **Actor / Role:** Cashier opens their own register session; Admin may open any.
- **Trigger:** Start of shift.
- **Preconditions:** The register must **not** already have an `OPEN` session.
- **Business flow:** select register → enter `opening_amount` (cash float) → open.
- **DB transaction:** `INSERT INTO cashier_sessions (cash_register_id, opened_by, opening_amount, status='OPEN')`.
- **Invariants:** at most one `OPEN` session per register (**BR-02**).
- **Errors / rollback:** double-open → rejected with "session already open".
- **Edge cases:** opening with `0` float is allowed but discouraged; opening float is excluded from sales totals but included in CASH expected value at close.

### F-12: Session Close / Cash Audit (Arqueo)

- **Actor / Role:** Cashier closes their own session; Admin can close any.
- **Trigger:** End of shift.
- **Preconditions:** Session is `OPEN`.
- **Business flow:**
  1. Cashier counts money by method and declares amounts (`cash_audits` rows).
  2. System computes **expected** per method: `CASH = opening_amount + Σ payments(CASH) from COMPLETED sales − Σ voids`; other methods = `Σ payments(method)`.
  3. Variance `declared − expected` shown per method.
  4. Confirm close.
- **DB transaction:**
  1. `INSERT INTO cash_audits` (one per declared method).
  2. `UPDATE cashier_sessions SET status='CLOSED', closing_amount=..., closed_at=now();`
  3. If any variance ≠ 0 → `INSERT INTO audit_log (...)` (**NFR-03**).
- **Invariants:** no sales may be created after close (session no longer `OPEN`); a positive variance means cash surplus, negative means shortage — never silently ignored.
- **Errors / rollback:** closing with pending un-voided discrepancies → allowed but audited; failure → rollback.
- **Edge cases:** forgotten sale → appears as variance; cashier mismatch → variance is attributed to the session, flagged for review.

### F-13: Authentication & Roles

- **Actor / Role:** All users; Admins manage users.
- **Trigger:** Login, or any protected action.
- **Preconditions:** User exists and `is_active = TRUE`.
- **Business flow:**
  1. Login: username + password → `bcrypt` verify → JWT issued with role claims.
  2. Every request passes middleware enforcing permissions.
- **Permission matrix (v1):**
  | Action | ADMIN | CASHIER | STOCK_CLERK |
  |---|---|---|---|
  | POS sale (F-07) | ✓ | ✓ | ✗ |
  | Own session open/close | ✓ | ✓ | ✗ |
  | Purchase reception (F-06) | ✓ | ✗ | ✓ |
  | Stock count / adjustments | ✓ | ✗ | ✓ |
  | Price/cost changes | ✓ | ✗ | ✗ |
  | Void (F-08) | ✓ | ✗ | ✗ |
  | Reports | ✓ | own-session | ✓ |
  | User management | ✓ | ✗ | ✗ |
- **DB:** `SELECT ... FROM users WHERE username = $1;` + bcrypt compare. Role enforcement is **server-side**, never client-side.
- **Invariants:** passwords stored only as bcrypt hashes; deactivated users cannot authenticate; every permission check is server-side (**NFR-02**).
- **Errors / rollback:** wrong password → generic "invalid credentials" (no user enumeration); deactivated → blocked.
- **Edge cases:** token expiry → re-login; password reset is admin-only (audited).

---

## 5. Reports (computed via queries — never stored)

### R-01: Replenishment (reorder list)
- **Semantics:** products where `is_active = TRUE` and `stock_balances.quantity <= reorder_point`.
- **Output:** product, current quantity, `reorder_point`, `max_stock`, preferred supplier (if set), suggested order qty (`min(max_stock − quantity, historical_velocity)`; capped by `max_stock` when configured).
- **Ordering:** most critical first (`quantity / reorder_point` ascending) → grouped by `preferred_supplier_id`.

### R-02: Expiring Soon
- **Semantics:** `product_batches` where `expiry_date <= now() + app_settings['expiry_warning_days']` and `remaining_quantity > 0`.
- **Output:** product, batch number, expiry date, `remaining_quantity`, current value at batch `unit_cost`.
- **Action link:** batch may be written off directly (F-09 known-exit with `reason = 'EXPIRATION'`).

### R-03: Top Products / ABC
- **Semantics:** aggregate `sale_items` by `product_id` for a period: units, revenue, margin (via `unit_cost` snapshots).
- **Output:** ranked list; A (top 80% revenue), B (next 15%), C (rest).

### R-04: Slow Movers ("hueso")
- **Semantics:** active products with zero `sale_items` in the period **and** `stock_balances.quantity > reorder_point`.
- **Output:** product, quantity on hand, value at cost. Purpose: detect capital locked in shelves.

### R-05: Inventory Valuation
- **Semantics:** `Σ stock_balances.quantity × current unit_cost` (per product), grouped by category.
- **Output:** total cost value on hand; per-category breakdown.

### R-06: Sales by Period & Payment Method
- **Semantics:** `sales` (`status = 'COMPLETED'`) grouped by day/hour, with `payments` split by method.
- **Output:** revenue per period, per method; average ticket; item count.

### R-07: Cash & Margin Report
- **Semantics:** per session: opening float, sales, voids, declared vs expected per method (from `cash_audits`), and margin = `Σ(sale_items.unit_price − unit_cost) × quantity`.
- **Output:** daily cash reconciliation + gross margin, using exact batch costs (**P-04**).

---

## 6. Concurrency & Integrity (cross-cutting)

- **Locking strategy:** every sale (F-07) locks `stock_balances` rows with `SELECT ... FOR UPDATE` **in ascending `product_id` order** before FIFO allocation. Consistent order is mandatory — out-of-order locking is the #1 source of deadlocks between two registers.
- **Negative stock prevention:** impossible at rest (application check + `CHECK (quantity >= 0)` + row locks). A sale that cannot be fully satisfied is rejected, not partially applied.
- **Append-only ledger:** `stock_movements`, `sale_items`, `payments`, `prices`, `product_costs` are insert-only. A DB **trigger raises an exception on `UPDATE`/`DELETE`** of `stock_movements` as defense-in-depth (**BR-05**).
- **Ledger ↔ balance consistency:** `Σ stock_movements.quantity = stock_balances.quantity` per `(product_id, store_id)`. Verified by an integrity query (see `04-manual-sql-testing.md`).
- **Timestamps:** all `created_at` are `TIMESTAMPTZ`; reports slice by server-local day.

---

## 7. Explicitly Deferred (not supported in v1)

The following are intentionally **out of scope** and must not be assumed to work:
- **Partial returns** (refund of a single line item) — v1 is full void only (F-08).
- **Consignment settlement** — `is_consignment` flag exists; payment/stock accounting for consigned goods is undefined in v1.
- **Inter-store transfers** — `TRANSFER` movement type exists in the enum but has no flow or tables.
- **Promotions / rule-based discounts** — only a header-level `discount_amount` is supported.
- **AFIP electronic invoicing** — internal tickets only.
- **GS1 scale barcode decoding** — manual weight entry only (F-10).
