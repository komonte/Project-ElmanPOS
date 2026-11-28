-- 0028_sale_returns.up.sql
-- Returns of part or all of a sale (any day). Decided with the store:
--   - Always from a sale (found by number): only what was sold, at its
--     price, never more than sold minus already returned.
--   - Per line, the returned goods go back to the shelf (a RETURN movement,
--     sale_id = the original sale, at the line cost; a combo gives back its
--     components) or are shrinkage (broken, expired: no stock movement, the
--     goods never come back).
--   - The money goes back by any method, split if needed, out of the
--     register's open session (its close expects less of that method).
--     ACCOUNT lowers the debt of the sale's customer.
--   - Admin only. A sale with returns cannot be voided.
CREATE TABLE sale_returns (
    id                 BIGSERIAL PRIMARY KEY,
    sale_id            BIGINT NOT NULL REFERENCES sales(id),
    cashier_session_id BIGINT NOT NULL REFERENCES cashier_sessions(id),
    reason             VARCHAR(300) NOT NULL CHECK (btrim(reason) <> ''),
    total              NUMERIC(12,2) NOT NULL CHECK (total > 0),
    created_by         BIGINT NOT NULL REFERENCES users(id),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_sale_returns_sale ON sale_returns (sale_id);
CREATE INDEX idx_sale_returns_session ON sale_returns (cashier_session_id);

CREATE TABLE sale_return_items (
    id           BIGSERIAL PRIMARY KEY,
    return_id    BIGINT NOT NULL REFERENCES sale_returns(id),
    sale_item_id BIGINT NOT NULL REFERENCES sale_items(id),
    quantity     NUMERIC(12,3) NOT NULL CHECK (quantity > 0),
    amount       NUMERIC(12,2) NOT NULL CHECK (amount >= 0),
    restock      BOOLEAN NOT NULL,
    UNIQUE (return_id, sale_item_id)
);
CREATE INDEX idx_sale_return_items_item ON sale_return_items (sale_item_id);

CREATE TABLE sale_return_payments (
    id        BIGSERIAL PRIMARY KEY,
    return_id BIGINT NOT NULL REFERENCES sale_returns(id),
    method    payment_method NOT NULL,
    amount    NUMERIC(12,2) NOT NULL CHECK (amount > 0),
    reference VARCHAR(120)
);
CREATE INDEX idx_sale_return_payments_return ON sale_return_payments (return_id);
