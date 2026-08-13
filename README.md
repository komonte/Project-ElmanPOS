# Elman-U POS - High concurrency Point of Sale & Inventory Management built in Go and PostgreSQL

# Context & Motivation

This project was born out of a real-world need: streamlining inventory control, price updating, and cashier auditing for a high-turnover local minimarket (my father's convenience store in Argentina).

Rather than building a synthetic demo app, **Elman POS** was engineered to handle production-grade constraints: fast barcode scanning, atomic sales processing to prevent stock mismatch, and ingestion of national retail datasets (70,000+ EAN-13 items).

## 1. Business Purpose
- This system is designed to solve high-rotation inventory control, expiration date tracking, and cash flow traceability for small-format retail.

## 2. Key Architecture Decisions

### Backend Architecture (Idiomatic Go)

Backend is organized within the `backend/` module using an idiomatic, layered Go layout:

- **`cmd/api/`**: Application entry point (`main.go`).
- **`internal/model/`**: Struct definitions (domain models and DTOs JSON).
- **`internal/store/`**: Persistence layer (native SQL with PostgreSQL / `pgx`).
- **`internal/service/`**: Core business rules and domain logic.
- **`internal/transport/`**: HTTP transport layer (handlers, routing, and JWT middleware)

## Technical Decisions
- **`Encapsulation`**: The `internal/` directory is strictly leveraged to prevent unauthorized external imports.
- **`Dependency Injection (DI)`**: Dependency flow is strictly unidirectional: `transport` → `service` → `store` → `DB`.
## 3. Tech Stack
...
## 4. Quick Start 
...
## 6. Project Documentation
...
