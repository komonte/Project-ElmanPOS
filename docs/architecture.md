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

### D-03: Layers and dependencies

```
transport → service/usecase → store (postgres) → DB
```

- `transport/` only knows `service/` (or `usecase/`)
- `service/` only knows `store/` (interfaces)
- `store/` only knows the DB (concrete implementation)
- `model/` is known by all layers (domain entities)

Manual dependency injection in `cmd/api/main.go` — no DI framework.

---

## Protocols

### P-01: Adding a new master entity

```
model/entity.go → store/postgres/entity_store.go → main.go: 5 lines
```

Steps:
1. Create `model/entity.go` — struct + `Validate()`
2. Create `store/postgres/entity_store.go` — implements `Store[Entity]` using
   `QueryContext`/`QueryRowContext`/`ExecContext` with explicit SQL
3. In `main.go`:
   ```go
   store := postgres.NewEntityStore(db)
   svc   := NewService[model.Entity](store, func(e *model.Entity) error { return e.Validate() })
   h     := NewResourceHandler[model.Entity](svc, "entity")
   mux.HandleFunc("/entities", h.HandleCollection)
   mux.HandleFunc("/entity/",  h.HandleEntity)
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