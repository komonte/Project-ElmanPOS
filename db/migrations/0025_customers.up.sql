-- 0025_customers.up.sql
-- Customers for selling on account ("fiado"). Only the name is required;
-- there is no credit limit. The name is unique ignoring case and spaces at
-- the ends, so the register does not open a second account for the same
-- person (two Marías are told apart in the name: "María (kiosco)").
-- A customer is deactivated, never deleted: sales will name it.
CREATE TABLE customers (
    id         BIGSERIAL PRIMARY KEY,
    name       VARCHAR(120) NOT NULL CHECK (btrim(name) <> ''),
    phone      VARCHAR(40),
    notes      VARCHAR(300),
    is_active  BOOLEAN NOT NULL DEFAULT TRUE,
    created_by BIGINT REFERENCES users(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX uq_customers_name_lower ON customers (lower(btrim(name)));
CREATE TRIGGER trg_customers_updated_at BEFORE UPDATE ON customers
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();
