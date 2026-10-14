include .env
export

DB_URL := postgresql://$(DB_USER):$(DB_PASSWORD)@localhost:$(DB_PORT)/$(DB_NAME)?sslmode=disable

.PHONY: help db-up db-down db-stop db-reset db-migrate-up db-migrate-down db-seed psql \
        build-ui build lint test dev check \
        api-brands api-taxes web-root web-brands web-taxes web-es

help:
	@echo "=== Database ==="
	@echo "  make db-up          Start Postgres container"
	@echo "  make db-down        Stop Postgres container"
	@echo "  make db-reset       Fresh DB + migrations + seed"
	@echo "  make db-migrate-up  Apply pending migrations"
	@echo "  make db-migrate-down Roll back last migration"
	@echo "  make db-seed        Load seed data"
	@echo "  make psql           Open psql shell"
	@echo ""
	@echo "=== Build ==="
	@echo "  make build-ui       Build Tailwind CSS"
	@echo "  make build          Build Go binary (includes build-ui)"
	@echo ""
	@echo "=== Quality ==="
	@echo "  make lint           Run linter (go vet)"
	@echo "  make test           Run all tests"
	@echo ""
	@echo "=== Dev Workflow ==="
	@echo "  make dev            DB up + build + start server"
	@echo "  make check          DB reset + build + vet + test"
	@echo ""
	@echo "=== Quick HTTP Tests ==="
	@echo "  make api-brands     GET /api/brands (JSON)"
	@echo "  make api-taxes      GET /api/taxes (JSON)"
	@echo "  make web-root       GET / (HTML dashboard)"
	@echo "  make web-brands     GET /brands (HTML)"
	@echo "  make web-taxes      GET /taxes (HTML)"
	@echo "  make web-es         GET / with lang=es cookie"

# ── Database ───────────────────────────────────────────────────────

## Start the Postgres container and wait until healthy
db-up:
	docker compose up -d --wait db

## Stop the container and remove the data volume (fresh state next up)
db-down:
	docker compose down -v

## Stop the container (keep data volume)
db-stop:
	docker compose down

## Full reset: fresh DB -> migrations applied -> seed loaded (reproducible)
db-reset: db-down db-up db-migrate-up db-seed
	@echo "DB reset OK"

## Apply pending migrations
db-migrate-up:
	migrate -path db/migrations -database "$(DB_URL)" up

## Roll back the last applied migration
db-migrate-down:
	migrate -path db/migrations -database "$(DB_URL)" down 1

## Load the dev seed (expects a freshly migrated DB)
db-seed:
	psql "$(DB_URL)" -v ON_ERROR_STOP=1 -f db/seed.sql

## Open a psql shell against the local DB
psql:
	psql "$(DB_URL)"

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

## Run all tests
test:
	cd backend && go test ./... -count=1

# ── Dev Workflow ──────────────────────────────────────────────────

## Start everything: DB + build + run server
dev: db-up build-ui
	@echo "Server starting at http://localhost:$(PORT)"
	cd backend && go run ./cmd/api

## Full check: fresh DB + build + lint + test
check: db-reset build lint test
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