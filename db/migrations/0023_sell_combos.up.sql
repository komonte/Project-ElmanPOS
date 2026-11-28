-- 0023_sell_combos.up.sql
-- Selling combos (COMPOSITE products, F-05 / F-07). A combo has no stock of
-- its own: a pack of cigarettes is 20 loose ones, a bag is 10 candies. Only
-- the loose product has stock, and each one has its own price.
--
-- 1. product_components: a component appears once per combo. Changing its
--    quantity replaces the row; it never adds a second one.
-- 2. sale_item_components: what each component gave to a combo line, copied
--    at the time of the sale (changing a recipe later does not touch past
--    sales). The stock movements of the components point to the sale, as
--    any other; uncovered_quantity is the part the system did not have
--    (startup mode, 0021), listed to count like sale_items.uncovered_quantity.
--    Component batches go to sale_item_batches of the combo line: the batch
--    says which product it is.

ALTER TABLE product_components
    ADD CONSTRAINT product_components_pack_component_key UNIQUE (pack_id, component_id);

CREATE TABLE sale_item_components (
    id                 BIGSERIAL PRIMARY KEY,
    sale_item_id       BIGINT NOT NULL REFERENCES sale_items(id),
    product_id         BIGINT NOT NULL REFERENCES products(id),
    quantity           NUMERIC(12,3) NOT NULL CHECK (quantity > 0),
    unit_cost          NUMERIC(12,2) NOT NULL,
    uncovered_quantity NUMERIC(12,3) NOT NULL DEFAULT 0,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT sale_item_components_uncovered_quantity_check
        CHECK (uncovered_quantity >= 0 AND uncovered_quantity <= quantity),
    UNIQUE (sale_item_id, product_id)
);
CREATE INDEX idx_sale_item_components_product_id ON sale_item_components (product_id);
CREATE INDEX idx_sale_item_components_uncovered ON sale_item_components (product_id)
    WHERE uncovered_quantity > 0;
