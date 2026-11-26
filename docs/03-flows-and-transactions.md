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
  1. Form: `name`, `description`, `brand` (active only), `category` (active only), `measure_unit`, `sale_type`, `controls_batches`, `shelf_life_days` (optional, 1–3650: prefills the expiry at reception, F-06), `reorder_point`, `max_stock`, `margin_percent`, `preferred_supplier_id` (active only), `is_consignment`, `is_active`. On create only, an optional **barcode** (F-02); on edit, barcodes have their own section.
     - The new-product form starts on the barcode field ("scan first"): scanning checks the code right away (`GET /products/barcode-check`); if it already belongs to a product, it says which and offers to edit that one instead of filling in the rest. Only a warning: it is checked again on save.
     - On create only, a missing **brand** or **category** can be created from the form, without going to their screens (otherwise the product tends to be left without brand or in "Sin Categorizar"):
       - Brand: the brand search offers "Create brand «X»" when the typed name is not exactly one of the listed brands (similar ones are listed first).
       - Category: "+ New category" opens a small form with name, parent (optional, active only) and VAT, prefilled from the parent's. Never from a name alone: the category sets the VAT of every sale.
  2. Save → system validates and persists (create: `INSERT`; edit: `UPDATE`).
  - **From a purchase line (F-06):** the same create form opens in a panel over the purchase (`product_form.gohtml` with `ForLine`; the same panel serves stock count lines, F-09). Differences: it saves to `POST /picker/quick-product` instead of `/products/new` and does not redirect (that would drop the half-loaded purchase): it closes and picks the new product in its line. It comes prefilled with what was typed in the line (a valid barcode goes to the code and the cursor to the name; anything else goes to the name and the cursor to the category) and the purchase's supplier as preferred supplier. The barcode check offers "Use that product in the line" instead of editing the owner (only if it can be received: active and not `COMPOSITE`). Same use case and transaction as below.
- **DB transaction:**
  1. Before `BEGIN`: validate the product, the new brand name and the new category (if any), then normalize the barcode (if any).
  2. `BEGIN;`
  3. If there is a barcode: `SELECT ... FROM product_codes WHERE code = $1` — if taken, `ROLLBACK` and report the owner product. Checked **before** the product `INSERT`, so a taken code does not use up a sku number, and the transaction is not left aborted (a failed `INSERT` aborts it and the owner could no longer be read).
  4. New brand: `SELECT ... FROM brands WHERE name = $1` (exact). Active → use it (someone may have created it meanwhile); **inactive → `ROLLBACK` and report it** (it is not reactivated: someone deactivated it on purpose); missing → `INSERT INTO brands`.
  5. New category: `INSERT INTO categories` with the same rules as the categories screen (tax required, active parent, name unique under the same parent).
  6. `INSERT INTO products (...)` or `UPDATE products SET ... WHERE id = $1;`
  7. Create with barcode: `INSERT INTO product_codes (...)`.
  8. `COMMIT;` — if any step fails, nothing is left: no product, brand, category or code.
  - *Implementation:* create goes through the use case `usecase/createproduct` (D-02) with a `TxRunner` and a `Request` (product, barcode, new brand, new category); edit stays in the generic service. Errors of the new category are wrapped in `createproduct.ErrNewCategory`, so the screen shows "New category: ..." (both have a name).
- **Invariants:**
  - `sku` unique (`UNIQUE(sku)`), generated by the DB on create (`next_product_sku()`, migration `0010`); never typed by the user.
  - Brand is chosen with a search box (thousands of brands), not a `<select>` (`architecture.md` D-05).
  - `max_stock` either `NULL` or strictly greater than `reorder_point` (`CHECK`).
  - Brand, category, and supplier selectors only display entities where `is_active = TRUE` (**BR-06**). The server rejects choosing an inactive one too; on edit, the product may **keep** the one it already had even if it was deactivated later.
  - Once the product has any `stock_movements` or `sale_items`, the fields `sale_type`, `controls_batches`, and `measure_unit` **cannot be changed** (changing them would corrupt past valuation). Only an `ADMIN` may change them after verifying zero stock, and it must be recorded in `audit_log`.
    - *Current implementation:* `purchase_items` and `product_batches` also count as history. The check runs inside the `UPDATE` itself (no gap between check and write). Until users and login exist, the change is **always** rejected (409); the `ADMIN` override is pending.
- **Errors / rollback:** duplicate `sku` → reject with message; any failure → full rollback.
- **Edge cases:**
  - Mass import (SEPA ETL): sets `reorder_point`, `margin_percent`, and category defaults in bulk; individual products are tuned later.
  - Deactivating (`is_active = FALSE`) keeps history intact; product disappears from POS search but remains in reports.

### F-02: Barcode Management

- **Actor / Role:** Admin (the `ADMIN` check is pending until login exists, F-13).
- **Trigger:** Admin adds or removes a barcode for a product (a code is never edited: remove it and add the right one).
- **Preconditions:** Product exists.
- **Business flow:** open the product's edit panel → "Barcodes" section → scan or type the code → it is saved right away (no need to press the product's Save). The **type is not chosen**: the server derives it (`EAN13`/`EAN8`/`INTERNAL`, see `02-data-model.md` §4.5). After adding, the cursor stays in the field to scan the next one. Removing asks for confirmation. A first code can also be given on create (F-01).
- **DB transaction:** single statements, no explicit transaction: `SELECT` the owner of the code (to name it if taken), then `INSERT`; or `DELETE ... WHERE id = $1 AND product_id = $2` (a code is only removed through its own product).
- **Invariants:** a code can never belong to two products (uniqueness guarantees the POS scan resolves unambiguously). Stored codes are digits only, zero-padded to 13 (`CHECK`).
- **Errors / rollback:** invalid code (non-digits, more than 14 digits, all zeros) → rejected; code already used → rejected, **naming the owner product** ("already belongs to 000123 COCA COLA 2L"). Two admins adding the same code at the same moment: `UNIQUE` rejects the second (without the owner's name).
- **From a purchase line (F-06):** a scanned code that is not in the catalog may belong to a product already there without it (imported catalog, new presentation). "Add this code to an existing product" opens a panel that searches receivable products (active, not `COMPOSITE`) by name or SKU; picking one runs the same `productcodes.Add` (`POST /picker/assign-code`) and picks the product in the line. Inactive or composite products are rejected even if posted by hand.
- **Interfaces:** web edit panel; API `GET/POST /api/products/{id}/codes` (body `{"code": "..."}`, the client never sends the type) and `DELETE /api/products/{id}/codes/{codeID}`. A taken code answers `409`.
- **Search:** the product search (web table, API `?search=`) also finds a product by its exact barcode, typed with or without the leading zeros. A partial code does not match.
- **Edge cases:**
  - Manufacturer changes packaging → add the new EAN to the **same** product; both codes scan to it.
  - Store-printed codes (`INTERNAL`) for composite packs and scale labels.
  - Deleting a product (only possible without history) deletes its codes (`ON DELETE CASCADE`, migration `0012`).

### F-03: Price Update

- **Actor / Role:** Admin.
- **Trigger:** Admin changes the sale price (PVP) of a product.
- **Preconditions:** Product exists and is active.
- **Business flow:** open the product's edit panel → "Sale price" section → enter the new `unit_price` → "Save price". It is saved right away (it does not wait for the product's "Save") and is **effective now**. The section shows the current price, its start date and the last 10 previous prices.
- **DB transaction** (`usecase/prices`, one `TxRunner.InTx`):
  1. Read the product: missing → 404; inactive → rejected (409).
  2. `INSERT INTO prices (product_id, unit_price, valid_from) VALUES (..., clock_timestamp())`.
  3. `INSERT INTO audit_log (user_id, 'prices', 'INSERT', price_id, new_data)` — price changes are audited (**NFR-03**). `new_data` is the saved price as JSON. `user_id` is `NULL` until there is a login (F-13).
- **Invariants:**
  - Rows are only appended: a change never edits or deletes an earlier price.
  - `UNIQUE(product_id, valid_from)` — no overlapping vigencias.
  - The current price is the row with the latest `valid_from` (one at any moment).
  - Past `sale_items` are never retroactively altered (**BR-03**).
- **Why `clock_timestamp()`:** `now()` is the transaction's start time, so two prices for the same product in one transaction would share `valid_from` and hit the `UNIQUE`. `clock_timestamp()` is the real time.
- **Validation:** `unit_price > 0`, at most 2 decimals, fits `NUMERIC(12,2)`. The web form takes a decimal comma (`1750,50`) or point; thousands separators (`1.750,50`) are rejected because `1.750` is ambiguous.
- **Errors / rollback:** invalid price → 400 (API) / message in the section (web); if the audit insert fails, the price is rolled back too.
- **Reading:** every product read (list, search, edit, API) carries the current price (`current_price`, `null` without one), from a `LEFT JOIN LATERAL` on the `(product_id, valid_from DESC)` index. The products table shows it in a "Price" column.
- **API:** `GET /api/products/{id}/prices` → `{current, history}` (newest first); `POST /api/products/{id}/prices` with `{"unit_price": "1500.50"}` (text or number). The client cannot send `valid_from`.
- **Edge cases:** cost-driven suggestion flow is F-04; bulk price updates by category are an extension (deferred); **scheduled prices** (a `valid_from` in the future) are deferred (§7).

### F-04: Cost Change → Average Cost & PVP Suggestion

- **Actor / Role:** Admin, or triggered automatically by F-06 (purchase reception).
- **Trigger:** A new `unit_cost` is recorded. **Normally it comes from a purchase (F-06)**, as stock arrives; the manual entry is an extra option (initial costs, a supplier price list, a correction).
- **Preconditions:** Product exists and is active.
- **Costs are without IVA.** Sale prices include IVA (§1), so the suggestion adds the IVA rate of the product's category. `unit_cost` and `avg_cost` are net amounts.
- **Two costs per row** (`model.Cost`):
  - `unit_cost` — the last (replacement) cost. The PVP suggestion uses it, because the sale price follows what the next unit will cost (one price per product, not per batch).
  - `avg_cost` — the weighted average cost of the stock on hand. Values the inventory and the real margin; never typed by the user.
- **Weighted average cost** (`model.AverageCost`):
  ```
  new_avg = (prev_avg × prev_qty_on_hand + new_cost × new_qty) / (prev_qty_on_hand + new_qty)
  ```
  where `prev_qty_on_hand` is the product's stock summed over all stores (`stock_balances`) before the new quantity is added, rounded to 2 decimals. Rules:
  - No previous cost, or no stock (`prev_qty_on_hand <= 0`) → `new_avg = new_cost` (nothing to average with).
  - Manual entry (`new_qty = 0`) with stock → `avg_cost` is **kept**: no goods came in at that cost. Only a purchase moves the average.
  - Purchase → the formula.
- **PVP suggestion** (`model.SuggestPrice`, read through `costs.Service.SuggestedPrice`):
  ```
  pvp_suggested = unit_cost × (1 + tax_rate/100) / (1 - margin_percent/100)
  ```
  where `tax_rate` is the rate of the product's category (`categories.tax_id → taxes.rate`). Computed in full and rounded once, to 2 decimals (rounding the net price first can move the cents: cost 1000, margin 30%, IVA 21% → 1728.57, not 1428.57 × 1.21 = 1728.67). The margin is over the price **without IVA**: cost 700, margin 30% → 1000 net → 1210 with IVA 21% (1105 with 10.5%). `margin_percent = 0` → no suggestion (manual pricing). The suggestion is **never saved**: the admin accepts it (which runs F-03) or ignores it.
- **Monotributo note:** a Responsable Inscripto recovers the purchase IVA, so the net cost is the real cost. A Monotributista does not: the real cost includes IVA. The suggestion is the same number either way (`cost × 1.21 / 0.7 = cost / 0.7 × 1.21`), but `avg_cost` would be below what was paid, so the margin reports (R-05, R-07) must account for the store's tax status.
- **Manual entry, business flow:** open the product's edit panel → "Cost" section (below "Sale price") → enter the new `unit_cost` → "Save cost". It is saved right away and is effective now. The section shows the last and average cost with their start date, the suggested price with the product's **saved** margin and a "Use as price" button (posts it to the price section, F-03), and the last 10 previous costs. With margin 0 it says why there is no suggestion.
- **Manual entry, DB transaction** (`usecase/costs`, one `TxRunner.InTx`):
  1. Read the product (for its margin): missing → 404; inactive → rejected (409).
  2. Lock and read the stock (`StockBalanceStore.LockOnHand`: the product row `FOR NO KEY UPDATE` and its balances `FOR UPDATE`, summed over all stores), **then** read the current cost (`CostStore.Current`). Locking first makes a manual entry wait for a reception of the same product in progress and read the cost it left; reading the cost first would keep the old average and save it over the new one.
  3. Compute `avg_cost` with `new_qty = 0`.
  4. `INSERT INTO product_costs (product_id, unit_cost, avg_cost, valid_from) VALUES (..., clock_timestamp())` — `clock_timestamp()` for the same reason as F-03 (a purchase with two lines of the same product saves two costs in one transaction).
  5. `INSERT INTO audit_log (user_id, 'product_costs', 'INSERT', cost_id, new_data)`; `user_id` is `NULL` until F-13.
- **From a purchase (F-06):** the same row and formula, inside the reception transaction, with `new_qty` = the received quantity. It locks the same way before reading the cost and the stock (`LockOnHand`), so two receptions at once, or a reception and a manual entry, do not average against stale data.
- **Validation:** `unit_cost > 0`, at most 2 decimals, fits `NUMERIC(12,2)`; the web form takes a decimal comma or point, like the price.
- **Invariants:** rows are only appended; the current cost is the latest `valid_from`; past margins are protected by line-item snapshots (**BR-03**).
- **Errors / rollback:** invalid cost → 400 / message in the section; if the audit insert fails, the cost is rolled back too.
- **API:** `GET /api/products/{id}/costs` → `{current, history}` (newest first); `POST /api/products/{id}/costs` with `{"unit_cost": "700"}` (text or number) → 201 `{cost, suggested_price}` (`null` with margin 0). The client cannot send `avg_cost` or `valid_from`.
- **Edge cases:** cost decreases → the suggestion drops; the admin decides. A category's IVA rate changes → the next suggestion uses the new rate; saved prices do not change. A cost for a product that never had stock sets the average directly, so a later purchase averages from it.

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

- **Actor / Role:** Stock Clerk (`STOCK_CLERK`) or Admin. Until F-13 there is no login: `received_by` and `performed_by` stay `NULL` (migration `0013`).
- **Trigger:** Goods arrive with a supplier invoice / remito.
- **Preconditions:** the supplier exists and is active; each product exists, is active and is not `COMPOSITE`; there is **exactly one active store** (`stores.is_active`), where the stock goes.
- **Amounts are without IVA:** `unit_cost` and the typed `total` are net amounts, like every cost (F-04).
- **Business flow** (Inventory → Purchases, `/purchases`):
  1. Choose the supplier (active ones only), type the voucher number (optional) and the invoice **subtotal without IVA**.
  2. Add lines. Each line has a product search (name, SKU or barcode, so a scanner works; combos are not offered), `quantity` and `unit_cost`. "+ Add line" and "Remove" change the lines in the page. Fully empty lines are ignored.
     - **Enter** in a line's search (a barcode scanner sends it after the code) never submits the purchase: it searches right away; with exactly one result it picks it and moves to the quantity; with none it opens the product create (below); with several, the list stays.
     - **Not in the catalog:** the search offers "+ Create «what was typed»" (the product form of F-01 in purchase mode) and, when what was typed is a barcode, "Add this code to an existing product" (F-02). The create panel links to the add-code panel too, since Enter on a scan opens the create directly. Closing a panel without picking returns the cursor to that line's search.
  3. Batch and expiry fields show up only for products with `controls_batches = TRUE`. Both are optional: an empty batch number gets an **automatic batch** (see the transaction). With a `shelf_life_days`, picking the product **prefills the expiry** with today + those days (the store PC's date), editable; a date typed by hand is kept, and one prefilled for another product is replaced when the product changes.
  4. The page shows the sum of the lines and its difference with the typed total as you type.
  5. "Confirm reception". On error the form comes back with everything typed, the message names the line ("Line 2: ...") and that line is marked.
  6. On success: purchase number and line count, a warning if the total does not match (§ decisions), and a **Prices** table (below).
- **Prices after receiving:** one row per product (in line order; a product on two lines shows the cost of the last one): new cost, average cost, suggested price with IVA (F-04) and current price. "Use as price" saves the suggestion as the new price with F-03 (`prices.Set`, audited) and redraws only that row's price cell. There is no button when the margin is 0 ("No margin") or the suggestion already is the current price. A failed save (e.g. the product was deactivated meanwhile) shows the error in the row and keeps the button. **No price changes on its own.**
- **DB transaction** (`usecase/purchases.Receive`, one `TxRunner.InTx`). Before it: `Purchase.Validate` (supplier set, voucher ≤ 60 chars, `0 <= total` with 2 decimals, at least one line; per line: product set, `quantity > 0` with ≤ 3 decimals, `unit_cost` with the cost rules, batch ≤ 80 chars, expiry a `YYYY-MM-DD` date; an expiry without a batch number is left to the product check below).
  1. Read the supplier: missing → 400; inactive → 409.
  2. Find the store: `StoreStore.ActiveID`. None or several active → 409 (stock is never put in a store picked at random).
  3. `INSERT INTO purchases (supplier_id, voucher_number, total, status='RECEIVED')`.
  4. For each line, in order (an error names the line: `line N: ...`):
     1. Read the product: missing → 400; inactive → 409; `COMPOSITE` → 400 (receive its components); batch rules: a batch or expiry on a product without `controls_batches` → 400.
     2. **Lock, then read:** `StockBalanceStore.LockOnHand` locks the product row (`FOR NO KEY UPDATE`) and its balances (`FOR UPDATE`) and returns the stock summed over all stores; **then** `CostStore.Current`. The product-row lock covers a product with no balance yet (no rows to lock). Another reception or a manual cost (F-04) of the same product waits and then reads the stock and cost this one left.
     3. If `controls_batches`: without a batch number, use `C-<purchase id>` (`purchases.AutoBatchNumber`: cold cuts often have no clear lot; the batch still drives FIFO and expiry and says which purchase it came from). Then `INSERT INTO product_batches (original_quantity = remaining_quantity = qty, unit_cost, batch_number, expiry_date)`, or add the quantity to a batch already created **in this reception** with the same product, batch number, expiry **and cost**. Same lot at a different cost → a separate batch (each batch keeps one cost for COGS, **P-04**).
     4. `INSERT INTO purchase_items (..., batch_id)`.
     5. `INSERT INTO stock_movements (type 'PURCHASE', +qty, unit_cost, batch_id, purchase_id, store_id)`.
     6. `stock_balances`: add `qty` for (product, store), creating the row if missing.
     7. `INSERT INTO product_costs (unit_cost, avg_cost)` with the F-04 formula, averaging against the stock read in step 2 (before this line). A product on two lines gets two costs; the second averages against the stock the first one left.
     8. Compute the suggested price (`costs.Suggest`, never saved).
  5. `INSERT INTO audit_log (user_id NULL, 'purchases', 'INSERT', purchase_id, new_data)` with the saved purchase and its lines.
  6. `COMMIT`.
- **Decisions:**
  - **One active store.** A purchase does not say which store it goes to; with one store there is nothing to choose. With two, the purchase will have to say it (and this check fails until then, on purpose).
  - **The typed total is a check, not a rule.** `total` is the invoice subtotal without IVA. If it does not match the sum of `quantity × unit_cost` (each line rounded to cents), the purchase is saved anyway and the screen/API warns with the difference (`total - sum`). Invoices round, add freight or discounts; blocking would stop the delivery at the door.
  - **Batch merging** only within one reception, and only with the same cost (see step 4.3). The same lot received in two purchases is two batches. Automatic batches merge the same way (same product, expiry and cost).
  - **Products can be created from the line** (F-01 form in purchase mode), outside the purchase transaction: the product exists even if the purchase is then abandoned.
  - **Combos are not received:** they have no stock of their own (F-05).
  - **Costs come from the purchase.** Each line saves a cost (F-04); prices never change on their own (see "Prices after receiving").
- **Invariants:** for every received unit, ledger, balance and batch `remaining_quantity` all increase by the same amount; `stock_movements` rows are never edited or deleted (trigger), so a received purchase cannot be undone by deleting it (a correction will be a count/adjustment, F-09).
- **Errors / rollback:** any failure → full rollback (no partial reception, no cost, no batch, no audit row). Validation → 400; inactive supplier/product, no/several active stores → 409; the message names the line when it is a line's fault.
- **API:**
  - `POST /api/purchases` with `{supplier_id, voucher_number, total, items: [{product_id, quantity, unit_cost, batch_number, expiry_date}]}` (numbers as text or number). Only what the user types: unknown fields (`status`, `batch_id`, `avg_cost`...) are rejected. → 201 `{purchase, total_difference, suggestions: [{product_id, product_name, unit_cost, avg_cost, suggested_price, current_price}]}` (`suggested_price` is `null` with margin 0; `total_difference` other than `"0"` is the warning).
  - `GET /api/purchases/{id}` → the purchase with its lines (batch number and expiry included). Missing → 404; bad id → 400.
- **Web routes:** `GET /purchases` (page), `POST /purchases` (receive; returns the result or the form with the error), `POST /purchases/prices/{id}` ("Use as price": runs `prices.Set` and returns the row's price cell; a separate route from `POST /products/{id}/prices` only because it renders a table cell instead of the product's price section), The line product picker is shared with stock counts (F-09): `GET /picker/product-options` (line search), `GET`/`POST /picker/quick-product` (create a product from a line, F-01), `GET /picker/assign-code`, `GET /picker/assign-code/options` and `POST /picker/assign-code` (add a code to an existing product, F-02). The create and add-code answers carry `HX-Trigger: productChosen {id, name, batches, shelf_life}`, which picks the product in the line that asked.
- **Edge cases:**
  - Non-batch products (soda): no `product_batches` row, no batch friction at reception.
  - Product missing from the catalog → create it from the line, or add the scanned code to the product it belongs to (business flow, step 2).
  - Purchase with a total of 0 (a free sample) is allowed; each line's cost still has to be > 0.
  - Consignment reception, returns to supplier and purchase orders: **deferred** (§7).

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

Also the **initial inventory**: the stock already on the shelves when the system starts is not loaded as a purchase, it is counted.

- **Actor / Role:** Stock Clerk counts; Admin confirms. Until F-13 there is no login: anyone can do both, and `stock_counts.created_by` and `performed_by` stay `NULL` (migration `0015`, like `0013`).
- **Trigger:** periodic physical inventory (weekly / monthly), full or by sectors; the initial inventory.
- **Preconditions:** **exactly one active store** (as F-06): the count is for it. At most **one `DRAFT` count per store** (partial unique index): the screen always continues the open one. No restriction on concurrent purchases (or, later, sales): see the snapshot below.
- **Business flow** (Inventory → Stock count, `/counts`):
  1. Without an open count the page only offers "Start a count" (looking at the page creates nothing).
  2. **Count by sectors** ("Aisle 3", "Storeroom"; free text with the sectors already used as suggestions). Type the sector, then scan or search the product with the same line picker as purchases (Enter on a scan picks it; not in the catalog → create it with the F-01 form or add the code to an existing product, F-02), type the quantity (kg for `WEIGHT` products, up to 3 decimals; **0 is valid**: "I looked here and there is none") and press Enter. Each load is a `stock_count_entries` row; the **same product in two sectors adds up**. The sector stays for the next product and the cursor goes back to the search.
  3. The sheet lists each counted product with its loads per sector (each removable with ×) and its total. **It does not show what the system says** (blind count): the comparison is in the review. The count can be left halfway and continued another day, or discarded (deletes the draft, no stock changes).
  4. **Review** (`/counts/{id}/review`): for each product whose count differs from its snapshot, system / counted / difference. Products that match are only counted. For a product that **goes up**, an optional **cost** (without IVA, the F-04 rules) and, if it `controls_batches`, an optional **expiry**; both save when leaving the field. A warning tells how many go up without a cost (they come in at today's average; those that never had a cost are valued at 0).
  5. **Confirm** (asks first: it cannot be undone). The review stays as a read-only record with the confirmation date.
- **Snapshot:** the first load of a product in the count creates its `stock_count_items` row with `system_quantity` = the product's balance in the store at that moment. Later loads only add to `counted_quantity`. Confirm moves the **difference** `counted − snapshot` on the live balance; it does **not** set the balance to the counted quantity. Goods received (or sold) after the product was counted are therefore kept. If nothing moved in between, both give the same result.
- **DB transaction** (`usecase/stockcounts.Confirm`, one `TxRunner.InTx`):
  1. `StockCountStore.LockDraft`: `SELECT ... FROM stock_counts WHERE id = $1 FOR UPDATE`; missing → 404, already `CONFIRMED` → 409. Every write on a count (load, remove, cost, discard) takes this lock first, so a load cannot slip in while another confirms.
  2. Read the items (with product name, `controls_batches` and current average cost).
  3. For each item with `counted − system ≠ 0` (an error names the product):
     1. **Lock, then read** (as F-06): `LockOnHand` (product row and balances), then the store balance, the product and `CostStore.Current`.
     2. If `store balance + difference < 0` → 409 `ErrNegativeStock` ("count the product again"); the whole confirm rolls back. `stock_balances.quantity >= 0` is also a DB `CHECK`.
     3. **Goes up:** unit cost = the typed cost, else the current average, else 0. If `controls_batches`: `INSERT INTO product_batches` with `batch_number = I-<count id>` (`stockcounts.AutoBatchNumber`), the typed expiry and that cost. One `stock_movements (type 'ADJUSTMENT', reason 'COUNT', +difference, unit_cost, batch_id, count_id)`. With a typed cost, `INSERT INTO product_costs` with the F-04 average against the stock read in 3.1 (like a purchase); without one the average does not change.
     4. **Goes down:** if `controls_batches`, take from the batches FIFO by expiry (`BatchStore.TakeFIFO`: earliest expiry first, batches without expiry last; a batch left at 0 is `is_consumed`), one movement per batch at that batch's cost; what the batches do not cover (stock from before the product controlled batches) is one movement without batch at the current average. Without batches, one movement at the current average.
     5. `stock_balances`: add the (signed) difference.
  4. `UPDATE stock_counts SET status = 'CONFIRMED', confirmed_at = now() WHERE id = $1 AND status = 'DRAFT'`.
  5. `INSERT INTO audit_log (user_id NULL, 'stock_counts', 'UPDATE', count_id, new_data)` with the confirmed count and its items.
  6. `COMMIT`.
- **Decisions (2026-11-26):**
  - **Products not counted are not touched** (never set to 0): a count can cover one sector, a category or the whole store, in parts.
  - **One draft at a time** per store; discarding it leaves no trace in the stock (its items and entries go with it, `ON DELETE CASCADE`). A confirmed count can never be deleted: its movements reference it.
  - **Inactive and `COMPOSITE` products cannot be counted** (reactivate the product; count a combo's components).
  - **Known exits** (own consumption, damage, expired goods) are logged on their own, any day, without a count (F-09b). They change the live balance, so the difference moved at confirm stays right when the exit happened **after** the product was counted; one that happened before counting but is logged after would be taken out twice, and F-09b warns about it.
  - Import of a count from a spreadsheet: later.
- **Invariants:**
  - After confirm, for every adjusted product: ledger, balance and batch `remaining_quantity` changed by the same amount; `balance = counted` when nothing else moved since the snapshot.
  - `DRAFT → CONFIRMED` is one-way; a count is confirmed at most once.
  - Reports (later) separate unexplained `COUNT` losses (true merma) from known exits (F-09b).
- **Errors / rollback:** any failure → full rollback (no movement, no batch, no cost, the count stays `DRAFT`). Bad load (no product, no sector, sector > 60 chars, negative quantity or > 3 decimals) → the sheet comes back with what was typed and the message.
- **Web routes:** `GET /counts` (the open draft, or "Start a count"), `POST /counts` (open or continue), `POST /counts/{id}/entries` (one load; returns the sheet), `POST /counts/{id}/entries/{entryID}/delete`, `POST /counts/{id}/discard`, `GET /counts/{id}/review`, `POST /counts/{id}/items/{productID}/details` (cost and expiry; returns the row and the summary out of band), `POST /counts/{id}/confirm`. The product search, quick create and add-code are the shared `/picker/...` routes (F-06). No JSON API yet.
- **Edge cases:**
  - **Initial inventory:** every product has snapshot 0, so all go up; typing the cost seeds the average cost (F-04).
  - **Positive difference** (more than the system says) is often a missing purchase; it gets a positive adjustment.
  - Product counted but not in the system → create it from the line (step 2).
  - A product that changed to `controls_batches` after it had stock: a negative difference larger than its batches takes the rest without batch.

### F-09b: Known Exits (own consumption, damage, expired goods)

- **Actor / Role:** Stock Clerk or Admin. Until F-13 there is no login: `created_by` and `performed_by` stay `NULL`.
- **Trigger:** goods leave the store without being sold: the family takes something, something breaks, something expires. Usually written on paper first and logged later.
- **Preconditions:** exactly one active store (as F-06); the product is active and not `COMPOSITE`; there is enough stock in the store.
- **Business flow** (Inventory → Stock exits, `/adjustments`), one exit per submit:
  1. Scan or search the product with the shared line picker (F-06; creating a product from here also works, though a new product has no stock to take).
  2. If the product has batches with stock, a **Batch** select lists them FIFO ("L-2475 · expires 2026-08-20 · 2 left"); the default is automatic (the one that expires first). For expired goods, pick the exact batch.
  3. If the product is already counted in the open count (F-09), a warning says so before logging (and again after): an exit that happened before it was counted must not be logged, it would be taken out twice.
  4. Type the quantity (kg for `WEIGHT` products, up to 3 decimals), pick the **reason** — Own consumption, Damage, Expired or Other — and an optional note (**required for Other**, max 500 chars). Enter logs it.
  5. The form comes back ready for the next one, with the same reason; the **latest 50 exits** are listed below (when, product, reason, quantity, note).
- **DB transaction** (`usecase/adjustments.Record`, one `TxRunner.InTx`). Before it: `StockAdjustment.Validate`.
  1. Find the store (`ActiveID`); read the product: missing → 400; inactive → 409; `COMPOSITE` → 400. A batch chosen for a product without `controls_batches` → 400.
  2. **Lock, then read** (as F-06): `LockOnHand`, then the store balance and `CostStore.Current`. Quantity greater than the store balance → 409 (`NotEnoughStockError`, the message says how much there is).
  3. `INSERT INTO stock_adjustments (store_id, product_id, reason, quantity, notes)`.
  4. Movements, all `ADJUSTMENT` with the exit's `reason` and `adjustment_id`, negative:
     - `controls_batches`: first the chosen batch (`BatchStore.TakeFrom`; a batch of another product → 400), then FIFO by expiry (`TakeFIFO`); one movement per batch at that batch's cost.
     - What the batches do not cover, or a product without batches: one movement without batch at the current average cost (0 if it never had one).
  5. `stock_balances`: subtract the quantity.
  6. `INSERT INTO audit_log (user_id NULL, 'stock_adjustments', 'INSERT', adjustment_id, new_data)`.
  7. `COMMIT`.
- **Decisions (2026-11-26):**
  - **Own table, any day.** Every movement needs exactly one source; a known exit is not a sale, a purchase or a count, so it has its own header row (`stock_adjustments`, migration `0016`) instead of hanging from an open count.
  - **Reasons:** `OWN_CONSUMPTION`, `DAMAGE`, `EXPIRATION`, `OTHER` (with a note). No `THEFT`: an unexplained loss shows up in a count (`COUNT`).
  - **The average cost does not change:** goods leave at their cost (batch or average).
  - **No edit, no undo:** `stock_adjustments` is append-only (trigger) and its movements cannot be deleted; a mistake is fixed with a count.
  - Products already counted in the open count: allowed, with a warning (above).
  - Merma report (explained vs. unexplained per period): later.
- **Invariants:** ledger, balance and batch `remaining_quantity` go down by the same amount; a known-exit movement is always `ADJUSTMENT` and negative (`CHECK`); the store balance never goes below 0.
- **Errors / rollback:** any failure → full rollback (no exit row, no movement, no batch change, no audit row). Bad input → the form comes back with what was typed and the message.
- **Web routes:** `GET /adjustments` (page), `POST /adjustments` (log; returns the form and the latest exits out of band), `GET /adjustments/product?product_id=` (the product's batches and the open-count warning). The search, quick create and add-code are the shared `/picker/...` routes. No JSON API yet.

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
- **Action link:** batch may be written off directly (F-09b known exit with `reason = 'EXPIRATION'` and that batch).

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
- **Purchase orders, returns to supplier, editing or voiding a received purchase** — a reception is final (F-06); a mistake is corrected with a stock count/adjustment (F-09) and a new cost.
- **Inter-store transfers** — `TRANSFER` movement type exists in the enum but has no flow or tables.
- **Promotions / rule-based discounts** — only a header-level `discount_amount` is supported.
- **AFIP electronic invoicing** — internal tickets only.
- **GS1 scale barcode decoding** — manual weight entry only (F-10).
- **Scheduled prices** — a price always takes effect when it is saved (F-03). Supporting a future `valid_from` needs the "current price" reads to filter `valid_from <= now()`.
