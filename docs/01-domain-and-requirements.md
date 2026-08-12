# 01. Domain & Requirements

## 1. System Overview
**Elman-U POS** is a lightweight, high-performance Point of Sale and inventory control solution build specificaly for small-format retail stores (convenience stores / minimarkets) in Argentina.

### 1.1 In-Scope (Phase 1)
- Multi-barcode product catalog (EAN-13 / internal codes).
- Cashier shift management (Opening float, ongoing sales, blind close & audit).
- Atomic checkout processing supporting cash, debit/credit cards, and transfer/QR.
- Append-only stock movement tracking and batch expiration handling.
- Product seeding from Argentina's national SEPA dataset.

### 1.2 Out-of-Scope (Deferred to Phase 2)
- Direct AFIP Electronic Invoicing integration (Internal receipts only).
- Automatic card terminal (POSNET) API integration.

---

## 2. User Roles & Access Control
- **'Admin'**: Full System access, price/cost management, manual stock overrides, financial reporting, and user administration.
- **'Cashier'**: POS operation, cash session management, barcode scanning, and basic product lookup.
- **'Stock Clerk'**: Supplier invoice processing, stock receiving, and expiration/spoilage adjustments.

--- 

## 3. Inviolable Business Rules

### BR-01: Financial Precision
All monetary operations and calculations must use arbitrary-precision decimals ('NUMERIC'). Floating-point arithmetic is strictly prohibited to avoid rounding errors.

### BR-02: Mandatory Open session
A sale cannot be initiated without an active, open cashier session ('cashier_session_id').

### BR-03: Historical Price & Cost Snapshot
When a sale is completed, 'unit_price' and 'unit_cost' must be snapshot directly into the 'sale_items' table. Future product price changes must never retroactively alter past revenue/margin reports.

### BR-04: Atomic Stock Operations
Sales processing and stock deductions must occur within a single database transaction ('BEGIN ... COMMIT'). If a payment fails, all inventory modifications must be rolled back.

### BR-05: Immutable Stock Ledger
Stock levels cannot be edited arbitrarily. Every change in inventory must generate a 'stock_movements' entry specifying movement type (sale, purchase, adjustment, expiration, return), user ID, and timestamp.

--- 

## 4. Non-Functional Requirements (NFR)
- **NFR-01 (Latency):** Barcode scan and product query latency must remain under 100ms.
- **NFR-02 (Data Integrity):** Relational integrity must be enforced directly at the database engine using foreign keys, check constraints, and unique indices.
- **NFR-03 (Auditability):** Critical system actions (price modifications, stock overrides, cash discrepancies) must be logged with timestamp and user reference.
