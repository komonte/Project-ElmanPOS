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
- **`internal/transport/`**: HTTP transport layer (handlers, routing, and the login middleware: sessions stored in PostgreSQL, see F-13)

## Technical Decisions
- **`Encapsulation`**: The `internal/` directory is strictly leveraged to prevent unauthorized external imports.
- **`Dependency Injection (DI)`**: Dependency flow is strictly unidirectional: `transport` → `service` → `store` → `DB`.
### Architecture & Evolution

- **Master tables** (brands, taxes, categories, suppliers, etc.): generic `Service[T]` + `ResourceHandler[T]` for CRUD. Each entity has its own store with explicit SQL.
- **Complex business logic** (products, purchases, sales, inventory): use cases in `internal/usecase/` with their own handlers and stores.
- **Adding a new master entity**: create the model + store (SQL), then wire it in `main.go` in 2 lines. The service and HTTP handler are already generic.
- See `docs/architecture.md` for full decision records and protocols.

## 3. Tech Stack
...
## 4. Quick Start 
...
## 5. Running on the store network (HTTPS)

The store's network is shared with the household and with guests, so the app must be served over **HTTPS**: otherwise the login password travels unencrypted. The server does it by itself with `TLS_CERT_FILE` and `TLS_KEY_FILE` (both or neither; without them it serves plain HTTP, for development only).

With [mkcert](https://github.com/FiloSottile/mkcert), on the store's PC:

```sh
mkcert -install                                   # creates a local certificate authority and trusts it here
mkcert -cert-file elmanpos.pem -key-file elmanpos-key.pem 192.168.0.10 elmanpos.local localhost 127.0.0.1
```

Use the PC's real LAN IP (and/or name). Then, in `.env`:

```sh
TLS_CERT_FILE=/path/to/elmanpos.pem
TLS_KEY_FILE=/path/to/elmanpos-key.pem
```

and open `https://192.168.0.10:8080`. Every other device that uses the app (a second PC, a phone for stock counts) must trust the same authority: copy `rootCA.pem` from `mkcert -CAROOT` and install it there. **Never copy `rootCA-key.pem`**: with it anyone can make certificates the store's devices trust. The certificate files are git-ignored.

If only the store's PC uses the app, `LISTEN_HOST=127.0.0.1` keeps it off the network entirely.

### The database stays on the store's PC

`docker-compose.yml` publishes Postgres on `127.0.0.1` only: the other devices reach the data through the app, never straight to the database. Docker bypasses the host firewall (`ufw`), so that bind address is the only lock; do not change it back to `5432:5432`.

On the store's PC, `.env` gets a new database name (not `elman_u`, not `elman_demo`) and a long password of its own: `elman_u_dev` is the development one. Postgres reads `DB_PASSWORD` only when the volume is created; to change it later, also run `ALTER USER <user> PASSWORD '...'` inside the database.

Also set `ELMAN_ENV=store` in the root `.env`. Some make targets delete the database (`db-down` and `db-reset` remove the Docker volume) or write test rows into the `.env` database (`test`, `check`), and sales and stock movements can never be deleted. With `ELMAN_ENV=store` those targets refuse to run. To stop Postgres without losing anything: `make db-stop`.

### Backups

Sales, stock and costs cannot be rebuilt if the disk dies, so the store's PC copies the database every night:

```sh
make backup         # a copy now: before an update, after the initial count
make restore-test   # restore the newest copy into a throwaway DB and compare row counts
```

`scripts/backup.sh` writes `pg_dump -Fc` to `BACKUP_DIR` (default `~/elmanpos-backups`): `daily/` keeps 14 days, `monthly/` keeps the first copy of each month for good. Each copy is checked with `pg_restore --list` before it counts. With `BACKUP_OFFSITE` set (a synced folder, a mounted drive) every new copy also goes there; if that folder is missing the script fails, so the timer shows it. A copy that stays on the same disk is not enough: take one away at least once a week.

Every night at 23:30 with systemd (edit the paths and the user, a member of the `docker` group, first):

```sh
sudo cp scripts/systemd/elmanpos-backup.{service,timer} /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now elmanpos-backup.timer
systemctl list-timers elmanpos-backup.timer   # next run
journalctl -u elmanpos-backup                 # what each run did
```

Run `make restore-test` once after setting it up, and again every few months. To bring a copy back for real, stop the app first, then `pg_restore --clean --if-exists -d <db>` from inside the container.

### First login

Every page asks for a username and password. The seed users (`admin`, `cajero`, `stock`) have no real password yet: set one from the store's PC, then log in with it.

```sh
make passwd U=admin    # asks twice, hidden; at least 8 characters
```

After that, the admin manages everyone on the **Users** page: create users, change a name or role, deactivate (they are out at once) or reactivate, and reset a password. The command is still there for when no admin can log in; both close that user's open sessions. Five failed logins in a row lock a username for 5 minutes. Details in `docs/03-flows-and-transactions.md`, F-13.

## 6. Project Documentation
...
