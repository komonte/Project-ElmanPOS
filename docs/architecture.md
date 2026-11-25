# ElmanPOS — Architecture Decision Records

Architecture decisions, rationale, and protocols for keeping the project
consistent as it grows.

---

## Architecture Decisions

### D-01: Generics for master table CRUD

**Context**: The project has ~10 master tables (brands, taxes, categories,
suppliers, users, stores, cash_registers, etc.) with structurally identical
CRUD operations.

**Decision**: Use generic `Service[T]` + `ResourceHandler[T]` to eliminate
service and handler boilerplate. The store layer stays explicit (raw SQL)
because each entity has different columns and constraints.

**Consequences**:
- Adding a new master entity = ~50 lines of store code + 2 lines in `main.go`
- HTTP behavior (status codes, errors, response format) is identical across entities
- Entities needing extra logic get a dedicated use case

### D-02: Use cases for complex business logic

**Context**: Operations like "create product with initial price", "record sale
with discount", "adjust stock" involve multiple entities and business rules.

**Decision**: These operations do NOT go through the generic layer. They live in
`backend/internal/usecase/` with their own handler, service, and store as needed.
The generic layer covers flat CRUD only.

**Consequences**:
- Clear boundary: if the operation touches >1 table or has a condition beyond
  simple field validation → use case
- Use cases can orchestrate multiple stores and services

**Implementation** (first use cases: barcodes, F-02):
- One package per use case: `usecase/productcodes` (list/add/remove a
  product's barcodes) and `usecase/createproduct` (product + optional
  barcode, new brand and new category in one transaction, F-01). Prices
  (F-03) are `usecase/prices`: `Set` (price + audit row in one transaction)
  and `History`. When a use
  case takes several optional parts, it takes them in a request struct
  (`createproduct.Request`).
- A use case works on a **copy** of the entity it receives: if the
  transaction fails, the caller's data does not point to rows that were
  rolled back (the form comes back with what the user typed).
- A use case declares the small interfaces it needs (`ProductGetter`,
  `CodeStore`...) instead of taking whole stores; the postgres stores satisfy
  them, and tests can pass a fake.
- Errors come out as the generic service's (`service.ErrNotFound`,
  `ErrValidation`, `ErrAlreadyExists`...), so the API and the web map them the
  same way. An error may carry data (`productcodes.CodeTakenError` names the
  owner product) and still match `errors.Is(err, service.ErrAlreadyExists)`.
- **Transactions:** `postgres.TxRunner` runs a function inside one
  transaction and rolls it back on error: `InTx(ctx, func(tx DBTX) error)`.
  The use case builds its stores on `tx` (`NewProductStore(tx)`...), and may
  reuse the generic service on it. `DBTxRunner` opens a real `BEGIN/COMMIT`;
  `SavepointRunner` uses a `SAVEPOINT` inside the test transaction.
- Inside a transaction, a failed statement aborts it (Postgres accepts no more
  queries until `ROLLBACK`). Check what you need to report (e.g. who owns a
  code) **before** the write that may fail; the DB constraint stays as the
  guard for concurrent requests.

### D-03: Layers and dependencies

```
transport → service/usecase → store (postgres) → DB
```

- `transport/` only knows `service/` (or `usecase/`)
- `service/` only knows `store/` (interfaces)
- `store/` only knows the DB (concrete implementation)
- `model/` is known by all layers (domain entities)

Manual dependency injection in `cmd/api/main.go` — no DI framework.

### D-04: Soft delete via `is_active`

**Context:** Master/catalog tables (brands, taxes, categories, suppliers, stores, cash registers, users) can be referenced by transactional rows. Hard `DELETE` would either violate FK constraints or lose referential integrity. All of them carry `is_active` (categories, stores and cash registers since migration `0008`).

**Decision:** All master/catalog tables use `is_active BOOLEAN NOT NULL DEFAULT TRUE` as the single soft-delete mechanism. `deleted_at` is **not** used — the pattern is boolean, consistent with existing usage. Semantics:
- `GetAll` in stores filters `WHERE is_active = TRUE` by default
- Hard `DELETE` remains only for rows with zero FK references (orphaned records)
- Deactivation (`is_active = FALSE`) keeps history intact; entity disappears from selection UI but remains in reports and historical references

**Consequences:**
- Consistent query pattern across all master entities
- No cascade rules needed — FK constraints in PostgreSQL already prevent hard deletion of referenced rows
- UI shows deactivate/activate buttons instead of delete when records are in use
- Adding `is_active` to a new entity is a one-line migration + one field in the model struct
- Soft delete is part of the generic layer: `postgres.Store[T]` requires `GetAllIncludingInactive`,
  `SearchAllByName` and `SetActive`, and `service.Service[T]` exposes them as
  `GetAllIncludingInactive`, `SearchAllByName`, `Deactivate` and `Reactivate`.
  No per-entity service is needed just for soft delete.

### D-05: Large catalog volume

**Context:** The real catalog (SEPA dataset, see `db/import/`) has ~65,000
products and ~6,000 brands. Loading a whole table to show it, a `<select>`
with thousands of options, or an API list without a limit all stop working
at that size (the product table took 90 ms in Postgres alone and the product
form weighed 460 KB).

**Decision:**
- **Paginated admin lists.** `Store[T]` has `ListAll(ctx, query, limit)`: the
  first `limit` rows (active and inactive) and the total that match the search.
  It runs two queries (count + page) instead of `COUNT(*) OVER ()`, so the page
  can stop early on an ordered index. The web tables show 50 rows and a
  "showing 50 of N" notice; the search finds the rest.
- **Indexes for that access** (migration `0011`): a B-tree in the admin order
  (`is_active DESC, name[, sku]`) and `pg_trgm` GIN indexes for
  `name ILIKE '%...%'` on products and brands.
- **Search instead of `<select>`** for large master tables in forms (brands in
  the product form): an htmx search that returns up to 20 active matches.
  Small tables (categories, suppliers, taxes) keep their `<select>`.
- **API lists are paginated:** `GET /api/{entities}` returns at most
  `?limit=` rows (default 100, max 500), filtered with `?search=`, and the
  total in the `X-Total-Count` header. `?name=` is still the quick search
  among active rows.

**Consequences:**
- Measured with the real catalog: product table 90 ms → 0.06 ms, search
  "queso" 81 ms → 16 ms, product form 460 KB → 8 KB, `GET /api/products`
  ~30 MB → 45 KB.
- **Product search also matches an exact barcode** (normalized first). It is
  written `OR p.id = (SELECT product_id FROM product_codes WHERE code = $2)`:
  the code is `UNIQUE`, so Postgres runs the subquery once and compares an id.
  Measured with the real catalog: no slower than without it (list ~6 ms,
  "queso" ~19 ms from Go), a barcode ~3 ms. A correlated `EXISTS` took
  100–400 ms. `listPage` takes the filter arguments as a list for this.
- A new master store must implement `ListAll` (use `listPage` in `db.go`).
- Searches shorter than 3 characters cannot use the trigram index (they scan
  the table); still a few ms at this size.

---

## Protocols

### P-01: Adding a new master entity

```
model/entity.go → store/postgres/entity_store.go → main.go: 5 lines
```

Steps:
1. Create `model/entity.go` — struct + `Validate()`
2. Create `store/postgres/entity_store.go` — implements `Store[Entity]` using
   `QueryContext`/`QueryRowContext`/`ExecContext` with explicit SQL, including
   the soft-delete methods (D-04) and the paginated `ListAll` (D-05). Add `var _ Store[model.Entity] = (*EntityStore)(nil)`
   so a missing method fails at compile time. Unique-column errors wrap
   `store.ErrDuplicate` (e.g. `fmt.Errorf("name %w", ErrDuplicate)`) so the
   generic service maps them to `ErrAlreadyExists`.
3. In `main.go`:
   ```go
   store := postgres.NewEntityStore(db)
   svc   := NewService[model.Entity](store, func(e *model.Entity) error { return e.Validate() })
   h     := NewResourceHandler[model.Entity](svc, "entity")
   h.Register(mux, "/api/entities")
   ```

### P-02: Entity deviates from standard CRUD

If `Entity` needs something the generic layer doesn't cover (e.g. `categories`
with tree operations, `users` with password hashing):

1. The basic CRUD (list, get-by-id, simple create) **still goes through the generic**
2. The extra operation goes in a **dedicated use case**

Example for users:
```
internal/usecase/create_user/
├── handler.go    → POST /users (with password hashing)
├── service.go    → orchestrates hashing + store.Create
└── store.go      → Store[User] (base CRUD only)
```

If the whole entity is radically different, drop the generic entirely and
implement it as a full use case.

### P-03: Commit convention

```
type(scope): english message

types: feat, refactor, fix, test, cleanup, docs
scope: store, service, transport, model, main

Examples:
  feat(store): add generic Store[T] interface
  refactor(store): standardize errors to ErrDuplicateName / ErrInUse
  cleanup: remove old brand_service.go and tax_service.go
```

### P-04: Tests

- **Generic service**: 1 suite testing with ≥2 concrete types (Brand + Tax)
- **Generic handler**: 1 suite testing with ≥2 concrete types
- **Specific store**: per-entity tests (real DB container or DB mock)
- **Use case**: specific tests for the use case

---

## References

- Clean Architecture (Robert Martin) — layer separation
- SOLID — Open/Closed: generic is open for extension (new entities),
  closed for modification (no changes to service/handler needed)
- Project docs: `README.md` in the repo