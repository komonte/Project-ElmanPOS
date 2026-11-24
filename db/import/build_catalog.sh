#!/usr/bin/env bash
# build_catalog.sh: arma un catálogo limpio (catalog_clean.sql) a partir del
# dump crudo (catalog_seed.sql).
#
#   1. Crea una base temporal (elman_import) en el mismo Postgres de la app.
#   2. Le aplica todas las migraciones: el resultado se valida contra el
#      esquema y las restricciones reales.
#   3. Carga las filas crudas en un schema "staging" sin restricciones.
#   4. Corre clean_catalog.sql (limpia y pasa a las tablas reales).
#   5. Exporta los datos limpios y borra la base temporal.
#
# La base de la app no se toca. Uso (desde la raíz del repo):
#   db/import/build_catalog.sh [dump_crudo] [salida]
set -euo pipefail

cd "$(dirname "$0")/../.."
RAW="${1:-../productos_dataset/catalog_seed/catalog_seed.sql}"
OUT="${2:-../productos_dataset/catalog_clean.sql}"
TMP_DB=elman_import
DB_CONTAINER="${DB_CONTAINER:-elman_u_db}"

set -a; . ./.env; set +a
export PGHOST=localhost PGPORT="$DB_PORT" PGUSER="$DB_USER" PGPASSWORD="$DB_PASSWORD"
URL="postgresql://$DB_USER:$DB_PASSWORD@localhost:$DB_PORT/$TMP_DB?sslmode=disable"

echo "1/5 base temporal $TMP_DB"
psql -d "$DB_NAME" -qc "DROP DATABASE IF EXISTS $TMP_DB;" -c "CREATE DATABASE $TMP_DB;"
trap 'psql -d "$DB_NAME" -qc "DROP DATABASE IF EXISTS $TMP_DB;"' EXIT

echo "2/5 migraciones"
migrate -path db/migrations -database "$URL" up >/dev/null

echo "3/5 datos crudos"
psql -d "$TMP_DB" -q -v ON_ERROR_STOP=1 <<'SQL'
CREATE SCHEMA staging;
CREATE TABLE staging.brands (id bigint, name text);
CREATE TABLE staging.taxes (id bigint, name text, rate numeric);
CREATE TABLE staging.categories (id bigint, parent_id bigint, name text, tax_id bigint);
CREATE TABLE staging.products (
    id bigint, sku text, name text, description text, brand_id bigint, category_id bigint,
    measure_unit text, sale_type text, controls_batches boolean, reorder_point numeric,
    max_stock numeric, margin_percent numeric, preferred_supplier_id bigint,
    is_consignment boolean, is_active boolean, created_at timestamptz);
SQL
# Solo las filas de estas 4 tablas; el resto del dump (usuarios de prueba,
# local, caja, settings) no se importa.
grep -E '^INSERT INTO public\.(brands|taxes|categories|products) ' "$RAW" \
    | sed 's/^INSERT INTO public\./INSERT INTO staging./' \
    | psql -d "$TMP_DB" -q -1 -v ON_ERROR_STOP=1

echo "4/5 limpieza"
psql -d "$TMP_DB" -q -1 -v ON_ERROR_STOP=1 -f db/import/clean_catalog.sql

echo "5/5 exportando a $OUT"
{
    cat <<'HEADER'
-- catalog_clean.sql: catálogo inicial limpio (generado por db/import/build_catalog.sh).
-- Cargar en una base NUEVA, con todas las migraciones y SIN db/seed.sql
-- (el seed de desarrollo choca con estos ids y nombres):
--   psql -1 -v ON_ERROR_STOP=1 -d elman_u -f catalog_clean.sql
HEADER
    # pg_dump del contenedor: misma versión que el servidor. Uno más nuevo
    # escribe opciones (ej. transaction_timeout) que Postgres 16 no conoce.
    docker exec "$DB_CONTAINER" pg_dump -U "$DB_USER" -d "$TMP_DB" --data-only --inserts --rows-per-insert=500 --no-owner \
        -t public.taxes -t public.categories -t public.brands \
        -t public.products -t public.product_codes
} > "$OUT"

psql -d "$TMP_DB" -At -F ' ' <<'SQL'
SELECT 'marcas:', count(*) FROM public.brands;
SELECT 'productos:', count(*) FROM public.products;
SELECT 'productos a granel:', count(*) FROM public.products WHERE sale_type = 'WEIGHT';
SELECT 'productos sin marca:', count(*) FROM public.products WHERE brand_id IS NULL;
SELECT 'codigos:', count(*) FROM public.product_codes;
SELECT 'codigos ' || code_type || ':', count(*) FROM public.product_codes GROUP BY code_type ORDER BY 1;
SQL
echo "listo: $OUT"
