-- 0004_sales.up.sql
-- Sales domain: cashier sessions, sales, line items, FIFO allocations, payments, audits.
-- 1:1 from docs/02-data-model.md.

-- ---- Enumerations (sales / cash) ----
CREATE TYPE payment_method AS ENUM ('CASH', 'DEBIT_CARD', 'CREDIT_CARD', 'TRANSFER', 'QR');
CREATE TYPE sale_status    AS ENUM ('COMPLETED', 'VOIDED');
CREATE TYPE session_status AS ENUM ('OPEN', 'CLOSED');

-- ---- cashier_sessions (02 4.15, BR-02) ----
CREATE TABLE cashier_sessions (
    id               BIGSERIAL PRIMARY KEY,
    cash_register_id BIGINT NOT NULL REFERENCES cash_registers(id),
    opened_by        BIGINT NOT NULL REFERENCES users(id),
    opening_amount   NUMERIC(12,2) NOT NULL CHECK (opening_amount >= 0),
    closing_amount   NUMERIC(12,2),
    status           session_status NOT NULL DEFAULT 'OPEN',
    opened_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    closed_at        TIMESTAMPTZ
);
CREATE INDEX idx_cashier_sessions_register_status ON cashier_sessions (cash_register_id, status);

-- ---- sales (02 4.16, BR-04) ----
CREATE TABLE sales (
    id                 BIGSERIAL PRIMARY KEY,
    cashier_session_id BIGINT NOT NULL REFERENCES cashier_sessions(id),
    sale_number        INTEGER NOT NULL,
    status             sale_status NOT NULL DEFAULT 'COMPLETED',
    subtotal           NUMERIC(12,2) NOT NULL CHECK (subtotal >= 0),
    tax_amount         NUMERIC(12,2) NOT NULL CHECK (tax_amount >= 0),
    discount_amount    NUMERIC(12,2) NOT NULL DEFAULT 0 CHECK (discount_amount >= 0),
    total              NUMERIC(12,2) NOT NULL CHECK (total > 0),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (cashier_session_id, sale_number)
);
CREATE INDEX idx_sales_created_at ON sales (created_at);
CREATE INDEX idx_sales_session_status ON sales (cashier_session_id, status);

-- ---- sale_items (02 4.17, BR-03 snapshots) ----
CREATE TABLE sale_items (
    id         BIGSERIAL PRIMARY KEY,
    sale_id    BIGINT NOT NULL REFERENCES sales(id),
    product_id BIGINT NOT NULL REFERENCES products(id),
    quantity   NUMERIC(12,3) NOT NULL CHECK (quantity > 0),
    unit_price NUMERIC(12,2) NOT NULL CHECK (unit_price > 0),
    unit_cost  NUMERIC(12,2) NOT NULL,
    tax_rate   NUMERIC(5,2) NOT NULL
);
CREATE INDEX idx_sale_items_sale_id ON sale_items (sale_id);
CREATE INDEX idx_sale_items_product_id ON sale_items (product_id);

-- ---- sale_item_batches (02 4.18, P-04 FIFO allocation) ----
CREATE TABLE sale_item_batches (
    id           BIGSERIAL PRIMARY KEY,
    sale_item_id BIGINT NOT NULL REFERENCES sale_items(id),
    batch_id     BIGINT NOT NULL REFERENCES product_batches(id),
    quantity     NUMERIC(12,3) NOT NULL CHECK (quantity > 0),
    unit_cost    NUMERIC(12,2) NOT NULL
);
CREATE INDEX idx_sale_item_batches_sale_item_id ON sale_item_batches (sale_item_id);
CREATE INDEX idx_sale_item_batches_batch_id ON sale_item_batches (batch_id);

-- ---- payments (02 4.19) ----
CREATE TABLE payments (
    id        BIGSERIAL PRIMARY KEY,
    sale_id   BIGINT NOT NULL REFERENCES sales(id),
    method    payment_method NOT NULL,
    amount    NUMERIC(12,2) NOT NULL CHECK (amount > 0),
    reference VARCHAR(120)
);
CREATE INDEX idx_payments_sale_id ON payments (sale_id);

-- ---- cash_audits (02 4.20, NFR-03 blind close) ----
CREATE TABLE cash_audits (
    id                 BIGSERIAL PRIMARY KEY,
    cashier_session_id BIGINT NOT NULL REFERENCES cashier_sessions(id),
    method             payment_method NOT NULL,
    declared_amount    NUMERIC(12,2) NOT NULL CHECK (declared_amount >= 0)
);
CREATE INDEX idx_cash_audits_session_method ON cash_audits (cashier_session_id, method);
