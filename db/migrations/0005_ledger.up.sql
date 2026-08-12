-- 0005_ledger.up.sql
-- Immutable ledger (P-01 / BR-05) and the cached balance aggregate.
-- Placed last among the domains it references: stock_movements FKs everything
-- (products, stores, batches, sales, purchases, stock_counts, users).
-- 1:1 from docs/02-data-model.md (02 4.12 / 4.13, 03 §6).

CREATE TABLE stock_movements (
    id            BIGSERIAL PRIMARY KEY,
    product_id    BIGINT NOT NULL REFERENCES products(id),
    store_id      BIGINT NOT NULL REFERENCES stores(id),
    batch_id      BIGINT REFERENCES product_batches(id),
    movement_type movement_type NOT NULL,
    reason        adjustment_reason,
    quantity      NUMERIC(12,3) NOT NULL CHECK (quantity <> 0),
    unit_cost     NUMERIC(12,2) NOT NULL,
    sale_id       BIGINT REFERENCES sales(id),
    purchase_id   BIGINT REFERENCES purchases(id),
    count_id      BIGINT REFERENCES stock_counts(id),
    performed_by  BIGINT NOT NULL REFERENCES users(id),
    notes         VARCHAR(500),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Exactly one source reference: no polymorphic FK (02 4.12)
    CHECK (num_nonnulls(sale_id, purchase_id, count_id) = 1),
    -- Sign per movement type (02 4.12)
    CHECK (
        (movement_type IN ('PURCHASE', 'RETURN') AND quantity > 0)
        OR (movement_type = 'ADJUSTMENT')
        OR (movement_type IN ('SALE', 'EXPIRATION', 'TRANSFER') AND quantity < 0)
    )
);
CREATE INDEX idx_stock_movements_product_created ON stock_movements (product_id, created_at);
CREATE INDEX idx_stock_movements_batch_id ON stock_movements (batch_id);
CREATE INDEX idx_stock_movements_store_created ON stock_movements (store_id, created_at);
CREATE INDEX idx_stock_movements_movement_type ON stock_movements (movement_type);

-- BR-05 / P-01 defense-in-depth: the ledger is append-only (03 §6).
CREATE FUNCTION prevent_stock_movements_write() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'stock_movements is append-only (BR-05): % is forbidden', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_stock_movements_append_only
BEFORE UPDATE OR DELETE ON stock_movements
FOR EACH ROW EXECUTE FUNCTION prevent_stock_movements_write();

-- ---- stock_balances (02 4.13, cached aggregate; PK avoids duplicates at engine level) ----
CREATE TABLE stock_balances (
    product_id BIGINT NOT NULL REFERENCES products(id),
    store_id   BIGINT NOT NULL REFERENCES stores(id),
    quantity   NUMERIC(12,3) NOT NULL CHECK (quantity >= 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (product_id, store_id)
);
