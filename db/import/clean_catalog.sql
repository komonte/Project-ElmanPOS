-- clean_catalog.sql
-- Limpia el catálogo crudo (schema staging, cargado desde catalog_seed.sql) y
-- lo inserta en las tablas reales (schema public) de una base con todas las
-- migraciones aplicadas. Las restricciones de la DB (UNIQUE, FK, CHECK, ENUM)
-- validan el resultado: si algo no entra, falla todo el script.
--
-- Lo corre build_catalog.sh dentro de una transacción. No toca la base de la app.
--
-- Reglas (las mismas que model.Brand / model.Product.Validate):
--   Marcas
--     - Mayúsculas, sin espacios de más ni caracteres de control.
--     - Nombres sin letras ni números ("," "." "...") o de 1 letra, y las
--       marcas de relleno (S/D, GENERICO, SIN MARCA, -N) se descartan: sus
--       productos quedan sin marca.
--     - Repetidas tras normalizar ("DEL  VALLE" = "DEL VALLE"): queda la de id
--       más chico y sus productos pasan a esa.
--     - Marcas que ningún producto usa no se cargan.
--   Productos
--     - El sku del dataset es el código de barras: pasa a product_codes. El sku
--       nuevo es el id con ceros a la izquierda (000123), el mismo formato que
--       next_product_sku() (migración 0010); la secuencia queda después del
--       más alto, así los productos nuevos no chocan.
--     - Nombre en mayúsculas, sin espacios de más ni caracteres de control.
--     - La descripción del dataset es siempre igual al nombre: queda vacía (NULL).
--     - Mismo nombre y misma marca = mismo producto para quien lo busca: se
--       juntan en uno (el de id más chico) y se conservan todos los códigos.
--     - A granel (WEIGHT, en kg):
--         * si el nombre dice "X KG", "XKG", "EL KG", "POR KG" o "GRANEL",
--           venga como venga en el dataset;
--         * si el dataset dice KILOGRAM y el nombre no trae tamaño de envase
--           ("1KG", "400 GR", "PAQ-1-Kg"). Si lo trae es un paquete.
--       Todo lo demás se vende por unidad (UNIT, en unidades).
--   Códigos de barra
--     - Se guardan como vienen: 13 dígitos con ceros a la izquierda.
--     - Tipo: EAN8 si son 8 dígitos reales con dígito verificador correcto;
--       EAN13 si son 12-13 dígitos reales con verificador correcto y no es de
--       balanza (prefijo 2); INTERNAL todo lo demás (códigos cortos de
--       balanza o del comercio, verificador incorrecto).

-- ---- Funciones auxiliares (se borran al final) ----

-- tidy: mayúsculas, sin caracteres de control y sin espacios de más. Cuenta
-- como espacio lo mismo que strings.Fields de Go (unicode.IsSpace): el espacio
-- duro (U+00A0) y compañía, que el \s de Postgres no reconoce.
CREATE FUNCTION staging.tidy(s text) RETURNS text LANGUAGE sql IMMUTABLE AS $$
    SELECT upper(btrim(regexp_replace(
        regexp_replace(s, '[[:cntrl:]\u0085\u00A0\u1680\u2000-\u200A\u2028\u2029\u202F\u205F\u3000]', ' ', 'g'),
        '\s+', ' ', 'g')))
$$;

-- gtin_ok: dígito verificador GS1 (sirve para EAN-8, UPC-A y EAN-13).
CREATE FUNCTION staging.gtin_ok(c text) RETURNS boolean LANGUAGE sql IMMUTABLE AS $$
    SELECT c ~ '^[0-9]{8,14}$' AND
           (10 - (SELECT sum(substr(c, i, 1)::int * CASE WHEN (length(c) - i) % 2 = 1 THEN 3 ELSE 1 END)
                  FROM generate_series(1, length(c) - 1) i) % 10) % 10 = right(c, 1)::int
$$;

-- ---- Impuestos y categorías: van tal cual ----

INSERT INTO public.taxes (id, name, rate)
SELECT id, btrim(name), rate FROM staging.taxes;

INSERT INTO public.categories (id, parent_id, name, tax_id)
SELECT id, parent_id, btrim(name), tax_id FROM staging.categories;

-- ---- Marcas ----

CREATE TEMP TABLE brand_map AS
WITH b AS (
    SELECT id, staging.tidy(name) AS name FROM staging.brands
)
SELECT id AS old_id,
       name,
       CASE WHEN length(name) >= 2 AND name ~ '[[:alnum:]]'
                 AND name NOT IN ('S/D', 'GENERICO', 'GENERICA', 'SIN MARCA', 'S/M', '-N')
            THEN min(id) OVER (PARTITION BY name)
       END AS new_id   -- NULL: marca descartada
FROM b;

-- ---- Productos ----

CREATE TEMP TABLE product_map AS
WITH p AS (
    SELECT s.id,
           s.sku AS code,
           staging.tidy(s.name) AS name,
           bm.new_id AS brand_id,
           s.category_id,
           s.measure_unit,
           s.created_at
    FROM staging.products s
    LEFT JOIN brand_map bm ON bm.old_id = s.brand_id
)
SELECT p.*,
       min(id) OVER (PARTITION BY name, coalesce(brand_id, 0)) AS keep_id,
       CASE
           WHEN name ~ '\m(X|EL|POR) ?KG\M' OR name ~ 'GRANEL' THEN 'WEIGHT'
           WHEN measure_unit <> 'KILOGRAM' THEN 'UNIT'
           WHEN name ~ '[0-9]+([.,][0-9]+)? ?(G|GR|GRS|GRM|GRAMOS|KG|KGS|K|ML|CC|L|LT|LTS|UN|U)(\.|\s|$|-|\)|/)'
             OR name ~ '\mX ?[0-9]'
             OR name ~ '\m[A-Z]{3}-[0-9.]+-' THEN 'UNIT'
           ELSE 'WEIGHT'
       END AS sale_type
FROM p;

-- Marcas: solo las que usa algún producto que queda.
INSERT INTO public.brands (id, name)
SELECT DISTINCT bm.new_id, bm.name
FROM brand_map bm
WHERE bm.new_id = bm.old_id
  AND EXISTS (SELECT 1 FROM product_map pm WHERE pm.id = pm.keep_id AND pm.brand_id = bm.new_id);

-- Productos: uno por grupo (nombre + marca). A granel se mide en kg; por
-- unidad, en unidades.
INSERT INTO public.products (id, sku, name, description, brand_id, category_id,
                             measure_unit, sale_type, created_at)
SELECT id,
       lpad(id::text, greatest(6, length(id::text)), '0'),
       name,
       NULL,
       brand_id,
       category_id,
       (CASE sale_type WHEN 'WEIGHT' THEN 'KILOGRAM' ELSE 'UNIT' END)::measure_unit,
       sale_type::product_sale_type,
       created_at
FROM product_map
WHERE id = keep_id;

-- Códigos: todos, apuntando al producto que quedó del grupo.
INSERT INTO public.product_codes (product_id, code_type, code)
SELECT keep_id,
       (CASE
            WHEN length(ltrim(code, '0')) = 8 AND staging.gtin_ok(ltrim(code, '0')) THEN 'EAN8'
            WHEN length(ltrim(code, '0')) >= 12 AND staging.gtin_ok(code) AND ltrim(code, '0') !~ '^2'
                 THEN 'EAN13'
            ELSE 'INTERNAL'
        END)::product_code_type,
       code
FROM product_map;

-- ---- Secuencias: que los próximos ids sigan después de los cargados ----

DO $$
BEGIN
    PERFORM setval('public.taxes_id_seq', (SELECT max(id) FROM public.taxes));
    PERFORM setval('public.categories_id_seq', (SELECT max(id) FROM public.categories));
    PERFORM setval('public.brands_id_seq', (SELECT max(id) FROM public.brands));
    PERFORM setval('public.products_id_seq', (SELECT max(id) FROM public.products));
    -- El sku generado (migración 0010) sigue después del más alto cargado.
    PERFORM setval('public.products_sku_seq', (SELECT max(sku::bigint) FROM public.products WHERE sku ~ '^[0-9]{1,18}$'));
END $$;

-- ---- Chequeos: las reglas de Validate() que la DB no frena ----

DO $$
DECLARE n bigint;
BEGIN
    SELECT count(*) INTO n FROM public.brands
    WHERE name <> staging.tidy(name) OR length(name) NOT BETWEEN 2 AND 120;
    IF n > 0 THEN RAISE EXCEPTION '% brands break model.Brand rules', n; END IF;

    SELECT count(*) INTO n FROM public.products
    WHERE name <> staging.tidy(name) OR length(name) NOT BETWEEN 2 AND 200
       OR (sale_type = 'WEIGHT' AND measure_unit <> 'KILOGRAM')
       OR (sale_type = 'COMPOSITE' AND controls_batches);
    IF n > 0 THEN RAISE EXCEPTION '% products break model.Product rules', n; END IF;

    SELECT count(*) INTO n FROM public.products p
    WHERE NOT EXISTS (SELECT 1 FROM public.product_codes c WHERE c.product_id = p.id);
    IF n > 0 THEN RAISE EXCEPTION '% products without barcode', n; END IF;
END $$;

DROP FUNCTION staging.tidy(text);
DROP FUNCTION staging.gtin_ok(text);
