-- 0003_purchases.up.sql
-- Purchase domain: supplier receptions. 1:1 from docs/02-data-model.md (02 4.21 / 4.22).

CREATE TYPE purchase_status AS ENUM ('RECEIVED', 'VOIDED');

CREATE TABLE purchases (
    id             BIGSERIAL PRIMARY KEY,
    supplier_id    BIGINT NOT NULL REFERENCES suppliers(id),
    voucher_number VARCHAR(60),
    status         purchase_status NOT NULL DEFAULT 'RECEIVED',
    total          NUMERIC(12,2) NOT NULL CHECK (total >= 0),
    received_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    received_by    BIGINT NOT NULL REFERENCES users(id)
);

CREATE TABLE purchase_items (
    id          BIGSERIAL PRIMARY KEY,
    purchase_id BIGINT NOT NULL REFERENCES purchases(id),
    product_id  BIGINT NOT NULL REFERENCES products(id),
    quantity    NUMERIC(12,3) NOT NULL CHECK (quantity > 0),
    unit_cost   NUMERIC(12,2) NOT NULL CHECK (unit_cost > 0),
    batch_id    BIGINT REFERENCES product_batches(id)
);
CREATE INDEX idx_purchase_items_purchase_id ON purchase_items (purchase_id);
CREATE INDEX idx_purchase_items_product_id ON purchase_items (product_id);
