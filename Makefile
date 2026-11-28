include .env
export

DB_URL := postgresql://$(DB_USER):$(DB_PASSWORD)@localhost:$(DB_PORT)/$(DB_NAME)?sslmode=disable

.PHONY: help not-store db-up db-down db-stop db-reset db-migrate-up db-migrate-down db-seed psql passwd \
        build-ui build lint test dev check \
        api-brands api-taxes web-root web-brands web-taxes web-es

help:
	@echo "=== Database ==="
	@echo "  make db-up          Start Postgres container"
	@echo "  make db-stop        Stop Postgres container (keeps the data)"
	@echo "  make db-down        Stop Postgres AND DELETE ALL ITS DATA"
	@echo "  make db-reset       DELETE ALL DATA, then migrations + seed"
	@echo "  make db-migrate-up  Apply pending migrations"
	@echo "  make db-migrate-down Roll back last migration (may drop data)"
	@echo "  make db-seed        Load seed data"
	@echo "  make psql           Open psql shell"
	@echo "  make passwd U=admin Set a user's password (asks twice, hidden)"
	@echo ""
	@echo "=== Build ==="
	@echo "  make build-ui       Build Tailwind CSS"
	@echo "  make build          Build Go binary (includes build-ui)"
	@echo ""
	@echo "=== Quality ==="
	@echo "  make lint           Run linter (go vet)"
	@echo "  make test           Run all tests (they write to the .env database)"
	@echo ""
	@echo "=== Dev Workflow ==="
	@echo "  make dev            DB up + build + start server"
	@echo "  make check          DB reset (DELETES DATA) + build + vet + test"
	@echo ""
	@echo "With ELMAN_ENV=store in .env (the store's PC), the targets that delete"
	@echo "data or write test rows refuse to run: db-down, db-reset,"
	@echo "db-migrate-down, db-seed, test and check."
	@echo ""
	@echo "=== Quick HTTP Tests ==="
	@echo "  make api-brands     GET /api/brands (JSON)"
	@echo "  make api-taxes      GET /api/taxes (JSON)"
	@echo "  make web-root       GET / (HTML dashboard)"
	@echo "  make web-brands     GET /brands (HTML)"
	@echo "  make web-taxes      GET /taxes (HTML)"
	@echo "  make web-es         GET / with lang=es cookie"

# ── Database ───────────────────────────────────────────────────────

## Guard: on the store's PC (ELMAN_ENV=store in .env) the real data lives in
## this database. Sales and stock movements cannot be deleted, so the targets
## that wipe the database or write test rows into it stop here.
not-store:
	@if [ "$(ELMAN_ENV)" = "store" ]; then \
		echo "Refusing: ELMAN_ENV=store (this is the store's PC)."; \
		echo "This target deletes the database or writes test data into it."; \
		echo "To stop Postgres without deleting anything: make db-stop"; \
		exit 1; \
	fi

## Start the Postgres container and wait until healthy
db-up:
	docker compose up -d --wait db

## Stop the container and DELETE the data volume (fresh state next up)
db-down: not-store
	docker compose down -v

## Stop the container (keep data volume)
db-stop:
	docker compose down

## Full reset: fresh DB -> migrations applied -> seed loaded (reproducible)
db-reset: not-store db-down db-up db-migrate-up db-seed
	@echo "DB reset OK"

## Apply pending migrations
db-migrate-up:
	migrate -path db/migrations -database "$(DB_URL)" up

## Roll back the last applied migration
db-migrate-down: not-store
	migrate -path db/migrations -database "$(DB_URL)" down 1

## Load the dev seed (expects a freshly migrated DB)
db-seed: not-store
	psql "$(DB_URL)" -v ON_ERROR_STOP=1 -f db/seed.sql

## Open a psql shell against the local DB
psql:
	psql "$(DB_URL)"

## Set a user's password (F-13): make passwd U=admin
## (U, not USER: make already has USER = the shell's user.)
passwd:
	@test -n "$(U)" || { echo "usage: make passwd U=<username>"; exit 2; }
	cd backend && go run ./cmd/api passwd "$(U)"

# ── Build ─────────────────────────────────────────────────────────

## Build Tailwind CSS from source
build-ui:
	cd backend && npx @tailwindcss/cli -i cmd/api/static/css/input.css -o cmd/api/static/css/output.css

## Build the Go backend (includes UI build)
build: build-ui
	cd backend && go build -o bin/elmanpos ./cmd/api

# ── Quality ────────────────────────────────────────────────────────

## Run linter (go vet)
lint:
	cd backend && go vet ./...

## Run all tests (against the .env database: never the store's)
test: not-store
	cd backend && TEST_DATABASE_URL="$(DB_URL)" go test ./... -count=1

# ── Dev Workflow ──────────────────────────────────────────────────

## Start everything: DB + build + run server
dev: db-up build-ui
	@echo "Server starting at http://localhost:$(PORT)"
	cd backend && go run ./cmd/api

## Full check: fresh DB + build + lint + test
check: not-store db-reset build lint test
	@echo "All checks passed"

# ── Quick HTTP Tests ──────────────────────────────────────────────

api-brands:
	curl -s http://localhost:$(PORT)/api/brands | python3 -m json.tool

api-taxes:
	curl -s http://localhost:$(PORT)/api/taxes | python3 -m json.tool

web-root:
	curl -s http://localhost:$(PORT)/ | head -30

web-brands:
	curl -s http://localhost:$(PORT)/brands | head -30

web-taxes:
	curl -s http://localhost:$(PORT)/taxes | head -30

web-es:
	curl -s -b "lang=es" http://localhost:$(PORT)/ | head -30