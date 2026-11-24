# 06. Initial Catalog Import

How the real product catalog (~65,000 products, ~6,000 brands) gets into a
fresh ElmanPOS database. Only the **code** that cleans the data lives in the
repo (`db/import/`); the data itself never does (see `.gitignore`).

## 1. Files

| File | In repo | What it is |
|---|---|---|
| `../productos_dataset/catalog_seed/catalog_seed.sql` | no | Raw input: a `pg_dump --data-only` built from the SEPA price dataset. |
| `../productos_dataset/2026-08-12/comercio_*/*.csv` | no | The SEPA source (products and prices per store). Not used yet. |
| `db/import/build_catalog.sh` | yes | Runs the whole pipeline. |
| `db/import/clean_catalog.sql` | yes | The cleaning rules, in SQL. |
| `../productos_dataset/catalog_clean.sql` | no | Output: clean data, ready to load. |

## 2. Build the clean catalog

```sh
db/import/build_catalog.sh [raw_dump] [output]   # from the repo root, DB container up
```

1. Creates a temporary database `elman_import` in the app's Postgres.
2. Applies **all migrations**, so the result is checked against the real
   schema and constraints.
3. Loads the raw rows into a `staging` schema with no constraints. Only
   brands, taxes, categories and products are read; the dump's sample users,
   store, register and settings are ignored.
4. Runs `clean_catalog.sql` in one transaction. It fails as a whole if any
   row breaks a constraint or a `Validate()` rule it re-checks.
5. Exports the clean tables with the container's `pg_dump` (same version as
   the server; a newer local `pg_dump` writes settings Postgres 16 rejects)
   and drops the temporary database. The app's database is never touched.

## 3. Cleaning rules

The same rules as `model.Brand` / `model.Product.Validate()`:

| Data | Raw | Clean |
|---|---|---|
| Product `sku` | The **barcode**, zero-padded to 13 digits | Moved to `product_codes`. New sku = id with zeros (`000123`), the `next_product_sku()` format. |
| Barcode type | — | `EAN8` (8 real digits, valid check digit), `EAN13` (12–13 digits, valid, not the in-store prefix `2`), else `INTERNAL` (scale / store codes, bad check digit). Stored as given: 13 digits, zero-padded. |
| Ids | Brands start at 21952 with gaps | **Kept**, so product → brand links stay. Sequences (ids and sku) are moved past the highest value. |
| Brand names | Double spaces, no-letter names (`.`, `,`), placeholders (`S/D`, `GENERICO`, `SIN MARCA`) | Uppercase, tidy. Duplicates after tidying merge into the lowest id. Invalid and placeholder brands are dropped: their products get no brand. Brands no product uses are not loaded. |
| Product names | Lowercase, extra / non-breaking spaces, control chars | Uppercase and tidy, counting as space what Go's `strings.Fields` does (incl. U+00A0). |
| Description | Always equal to the name | `NULL`. |
| Same name + same brand | Several rows (e.g. a towel in 61 colors) | **One product with all its barcodes.** To someone searching they are indistinguishable. |
| Sold by weight | `KILOGRAM` is unreliable (also 1 kg packs) | `WEIGHT` (in kg) if the name says `X KG` / `EL KG` / `POR KG` / `GRANEL`, or it is `KILOGRAM` with no package size in the name; else `UNIT` (in units). Heuristic: a wrong one is fixed by hand (allowed while the product has no stock history). |
| Category | Single `Sin Categorizar` | Kept. Categorizing is a later task. |

Result with the 2026-08-12 dataset: 6,224 brands, 64,602 products (2,728 by
weight), 67,439 barcodes (60,660 EAN13, 412 EAN8, 6,367 INTERNAL). Every row
passes the Go `Validate()` (checked with a throwaway program).

The barcode rules exist twice: in `clean_catalog.sql` and in Go
(`model.NormalizeBarcode`, `model.BarcodeType`), which the app uses for codes
typed or scanned later. They must stay the same. Checked on 2026-11-24 with a
throwaway program: all 67,439 loaded codes are already normalized and get the
same type in Go. Migration `0012` also enforces the format in the DB
(13–14 digits), so a code the import gets wrong fails the load.

## 4. Load it

On a **new** database, with all migrations and **without** `db/seed.sql`
(the development seed collides with these ids and names):

```sh
make db-migrate-up
psql -1 -v ON_ERROR_STOP=1 -d elman_u -f ../productos_dataset/catalog_clean.sql
```

`-1` runs the whole file in one transaction: it loads completely or not at
all. After loading, the next product created in the app gets the next free
sku (e.g. `067440`).

## 5. Known limits

- Some "brands" are really product name fragments (`SHAMPOO DOVE OLEO NU`);
  they cannot be told apart automatically and stay as they are.
- All products land in one category.
- The SEPA CSVs carry prices per store; they could seed `prices` (F-03) later.
