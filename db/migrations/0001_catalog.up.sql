-- 0001_catalog.up.sql
-- Catalog domain: taxes, categories, brands, suppliers, users, products, and their
-- codes / components / price & cost history. 1:1 from docs/02-data-model.md.

-- ---- Enumerations (catalog) ----
CREATE TYPE product_sale_type AS ENUM ('UNIT', 'WEIGHT', 'COMPOSITE');
CREATE TYPE product_code_type AS ENUM ('EAN13', 'EAN8', 'INTERNAL');
CREATE TYPE measure_unit     AS ENUM ('UNIT', 'GRAM', 'KILOGRAM', 'LITER', 'MILLILITER', 'METER');
CREATE TYPE user_role        AS ENUM ('ADMIN', 'CASHIER', 'STOCK_CLERK');

-- ---- taxes (02 4.1) ----
CREATE TABLE taxes (
    id   BIGSERIAL PRIMARY KEY,
    name VARCHAR(80) NOT NULL,
    rate NUMERIC(5,2) NOT NULL CHECK (rate >= 0 AND rate <= 100)
);
CREATE UNIQUE INDEX idx_taxes_name_unique_lower ON taxes (LOWER(name));

-- ---- categories (02 4.2) ----
CREATE TABLE categories (
    id        BIGSERIAL PRIMARY KEY,
    parent_id BIGINT REFERENCES categories(id),
    name      VARCHAR(120) NOT NULL,
    tax_id    BIGINT NOT NULL REFERENCES taxes(id)
);
CREATE INDEX idx_categories_parent_id ON categories (parent_id);

-- ---- brands (02 4.3) ----
CREATE TABLE brands (
    id   BIGSERIAL PRIMARY KEY,
    name VARCHAR(120) NOT NULL UNIQUE
);

-- ---- suppliers (02 4.10; referenced by products.preferred_supplier_id) ----
CREATE TABLE suppliers (
    id            BIGSERIAL PRIMARY KEY,
    name          VARCHAR(160) NOT NULL,
    tax_id_number VARCHAR(20),
    phone         VARCHAR(40),
    email         VARCHAR(120),
    payment_terms VARCHAR(120),
    is_active     BOOLEAN NOT NULL DEFAULT TRUE
);

-- ---- users (02 4.23; referenced by every performed_by FK) ----
CREATE TABLE users (
    id            BIGSERIAL PRIMARY KEY,
    username      VARCHAR(60) NOT NULL UNIQUE,
    name          VARCHAR(120) NOT NULL,
    password_hash VARCHAR(120) NOT NULL,
    role          user_role NOT NULL,
    is_active     BOOLEAN NOT NULL DEFAULT TRUE
);

-- ---- products (02 4.4) ----
CREATE TABLE products (
    id                    BIGSERIAL PRIMARY KEY,
    sku                   VARCHAR(40) NOT NULL UNIQUE,
    name                  VARCHAR(200) NOT NULL,
    description           VARCHAR(500),
    brand_id              BIGINT REFERENCES brands(id),
    category_id           BIGINT NOT NULL REFERENCES categories(id),
    measure_unit          measure_unit NOT NULL DEFAULT 'UNIT',
    sale_type             product_sale_type NOT NULL DEFAULT 'UNIT',
    controls_batches      BOOLEAN NOT NULL DEFAULT FALSE,
    reorder_point         NUMERIC(12,3) NOT NULL DEFAULT 0 CHECK (reorder_point >= 0),
    max_stock             NUMERIC(12,3) CHECK (max_stock IS NULL OR max_stock > reorder_point),
    margin_percent        NUMERIC(5,2) NOT NULL DEFAULT 0 CHECK (margin_percent >= 0),
    preferred_supplier_id BIGINT REFERENCES suppliers(id),
    is_consignment        BOOLEAN NOT NULL DEFAULT FALSE,
    is_active             BOOLEAN NOT NULL DEFAULT TRUE,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_products_category_id ON products (category_id);
CREATE INDEX idx_products_brand_id ON products (brand_id);
CREATE INDEX idx_products_is_active ON products (is_active);
CREATE INDEX idx_products_preferred_supplier_id ON products (preferred_supplier_id);

-- ---- product_codes (02 4.5) ----
CREATE TABLE product_codes (
    id         BIGSERIAL PRIMARY KEY,
    product_id BIGINT NOT NULL REFERENCES products(id),
    code_type  product_code_type NOT NULL,
    code       VARCHAR(14) NOT NULL UNIQUE
);
CREATE INDEX idx_product_codes_product_id ON product_codes (product_id);

-- ---- product_components (02 4.6) ----
CREATE TABLE product_components (
    id           BIGSERIAL PRIMARY KEY,
    pack_id      BIGINT NOT NULL REFERENCES products(id),
    component_id BIGINT NOT NULL REFERENCES products(id),
    quantity     NUMERIC(12,3) NOT NULL CHECK (quantity > 0),
    CHECK (pack_id <> component_id)
);
CREATE INDEX idx_product_components_pack_id ON product_components (pack_id);

-- ---- prices (02 4.7, BR-03 price history) ----
CREATE TABLE prices (
    id         BIGSERIAL PRIMARY KEY,
    product_id BIGINT NOT NULL REFERENCES products(id),
    unit_price NUMERIC(12,2) NOT NULL CHECK (unit_price > 0),
    valid_from TIMESTAMPTZ NOT NULL,
    UNIQUE (product_id, valid_from)
);
CREATE INDEX idx_prices_product_valid_from_desc ON prices (product_id, valid_from DESC);

-- ---- product_costs (02 4.7, BR-03 cost history) ----
CREATE TABLE product_costs (
    id         BIGSERIAL PRIMARY KEY,
    product_id BIGINT NOT NULL REFERENCES products(id),
    unit_cost  NUMERIC(12,2) NOT NULL,
    avg_cost   NUMERIC(12,2) NOT NULL,
    valid_from TIMESTAMPTZ NOT NULL,
    UNIQUE (product_id, valid_from)
);
CREATE INDEX idx_product_costs_product_valid_from_desc ON product_costs (product_id, valid_from DESC);
