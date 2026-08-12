include .env
export

DB_URL := postgresql://$(DB_USER):$(DB_PASSWORD)@localhost:$(DB_PORT)/$(DB_NAME)?sslmode=disable

.PHONY: db-up db-down db-reset db-migrate-up db-migrate-down db-seed psql lint test

## Start the Postgres container and wait until healthy
db-up:
	docker compose up -d --wait db

## Stop the container (data persists in the named volume `pgdata`)
db-down:
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

## (arrives with the Go backend) static analysis
lint:
	@echo "lint: disponible cuando exista el modulo Go (golangci-lint)"

## (arrives with the Go backend) tests
test:
	@echo "test: disponible cuando exista el modulo Go"
