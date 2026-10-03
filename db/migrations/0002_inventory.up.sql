-- 0002_inventory.up.sql
-- Inventory domain: stores, registers, batches, stock counts. 1:1 from docs/02-data-model.md.

-- ---- Enumerations (inventory) ----
CREATE TYPE movement_type     AS ENUM ('SALE', 'PURCHASE', 'RETURN', 'ADJUSTMENT', 'EXPIRATION', 'TRANSFER');
CREATE TYPE adjustment_reason AS ENUM ('COUNT', 'OWN_CONSUMPTION', 'DAMAGE', 'EXPIRATION', 'THEFT', 'OTHER');
CREATE TYPE count_status      AS ENUM ('DRAFT', 'CONFIRMED');

-- ---- stores (02 4.8) ----
CREATE TABLE stores (
    id         BIGSERIAL PRIMARY KEY,
    name       VARCHAR(120) NOT NULL,
    address    VARCHAR(200),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER trg_stores_updated_at BEFORE UPDATE ON stores
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---- cash_registers (02 4.9) ----
CREATE TABLE cash_registers (
    id         BIGSERIAL PRIMARY KEY,
    store_id   BIGINT NOT NULL REFERENCES stores(id),
    name       VARCHAR(80) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TRIGGER trg_cash_registers_updated_at BEFORE UPDATE ON cash_registers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---- product_batches (02 4.11, P-04 FIFO) ----
CREATE TABLE product_batches (
    id                 BIGSERIAL PRIMARY KEY,
    product_id         BIGINT NOT NULL REFERENCES products(id),
    batch_number       VARCHAR(80) NOT NULL,
    expiry_date        DATE,
    original_quantity  NUMERIC(12,3) NOT NULL CHECK (original_quantity > 0),
    remaining_quantity NUMERIC(12,3) NOT NULL,
    unit_cost          NUMERIC(12,2) NOT NULL,
    is_consumed        BOOLEAN NOT NULL DEFAULT FALSE,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (remaining_quantity >= 0 AND remaining_quantity <= original_quantity)
);
CREATE INDEX idx_product_batches_fifo ON product_batches (product_id, remaining_quantity, expiry_date);
CREATE TRIGGER trg_product_batches_updated_at BEFORE UPDATE ON product_batches
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---- stock_counts (02 4.14) ----
CREATE TABLE stock_counts (
    id           BIGSERIAL PRIMARY KEY,
    store_id     BIGINT NOT NULL REFERENCES stores(id),
    status       count_status NOT NULL DEFAULT 'DRAFT',
    created_by   BIGINT NOT NULL REFERENCES users(id),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    confirmed_at TIMESTAMPTZ
);
CREATE TRIGGER trg_stock_counts_updated_at BEFORE UPDATE ON stock_counts
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---- stock_count_items (02 4.14) ----
CREATE TABLE stock_count_items (
    id               BIGSERIAL PRIMARY KEY,
    count_id         BIGINT NOT NULL REFERENCES stock_counts(id),
    product_id       BIGINT NOT NULL REFERENCES products(id),
    system_quantity  NUMERIC(12,3) NOT NULL,
    counted_quantity NUMERIC(12,3) NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (count_id, product_id)
);
