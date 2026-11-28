-- 0027_customer_payments.up.sql
-- Collecting a debt ("fiado") at the register. The debt of a customer is
-- what was sold to them on account (ACCOUNT payments of COMPLETED sales)
-- minus these payments. A payment is drawer money: it goes to the session
-- that was open and adds to the expected amount of its method at the close.
-- Never ACCOUNT (paying a debt with more debt makes no sense). Rows are
-- only appended. No payment exceeds the debt (checked by the app with the
-- customer locked).
CREATE TABLE customer_payments (
    id                 BIGSERIAL PRIMARY KEY,
    customer_id        BIGINT NOT NULL REFERENCES customers(id),
    cashier_session_id BIGINT NOT NULL REFERENCES cashier_sessions(id),
    method             payment_method NOT NULL CHECK (method <> 'ACCOUNT'),
    amount             NUMERIC(12,2) NOT NULL CHECK (amount > 0),
    reference          VARCHAR(120),
    created_by         BIGINT NOT NULL REFERENCES users(id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_customer_payments_customer ON customer_payments (customer_id, created_at);
CREATE INDEX idx_customer_payments_session ON customer_payments (cashier_session_id);
