package postgres_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
)

// testTx abre una transacción contra la DB de desarrollo y la deshace al
// terminar el test: nada de lo que el test escribe queda guardado.
//
// La DB se toma de TEST_DATABASE_URL (`make test` la arma desde .env).
// Si no está definida, el test se saltea: `go test ./...` sigue andando
// sin la DB levantada. La DB tiene que tener las migraciones aplicadas.
//
// Ojo: en Postgres, cuando una sentencia falla, la transacción queda
// abortada y ya no acepta otras. Un test que espera un error de la DB
// (duplicado, FK) tiene que dejar esa sentencia para el final.
func testTx(t *testing.T) *sql.Tx {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; run `make test` with the DB up")
	}

	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx (is the DB up?): %v", err)
	}
	// Cleanup corre en orden inverso: primero el Rollback, después el Close.
	t.Cleanup(func() { tx.Rollback() })

	return tx
}
