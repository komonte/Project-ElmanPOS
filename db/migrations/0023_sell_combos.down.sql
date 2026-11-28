-- 0023_sell_combos.down.sql
-- Forgets what each component gave to past combo lines (their stock
-- movements stay) and allows a component twice in a combo again.

DROP TABLE sale_item_components;

ALTER TABLE product_components DROP CONSTRAINT product_components_pack_component_key;
