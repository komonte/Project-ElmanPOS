package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

var errFnFailed = errors.New("fn failed")

// insertBrand escribe una marca con lo que reciba fn: así se ve si la
// escritura quedó o se deshizo.
func insertBrand(ctx context.Context, tx postgres.DBTX, name string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO brands (name) VALUES ($1);`, name)
	return err
}

func countBrands(t *testing.T, db postgres.DBTX, name string) int {
	t.Helper()
	var n int
	err := db.QueryRowContext(context.Background(),
		`SELECT count(*) FROM brands WHERE name = $1;`, name).Scan(&n)
	if err != nil {
		t.Fatalf("count brands: %v", err)
	}
	return n
}

// DBTxRunner abre transacciones de verdad: el caso que guarda deja la marca
// en la DB, así que el test la borra al terminar.
func TestDBTxRunner(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	runner := postgres.NewTxRunner(db)
	const name = "MARCA TEST TX RUNNER"
	t.Cleanup(func() { db.ExecContext(ctx, `DELETE FROM brands WHERE name = $1;`, name) })

	t.Run("error rolls back", func(t *testing.T) {
		err := runner.InTx(ctx, func(tx postgres.DBTX) error {
			if err := insertBrand(ctx, tx, name); err != nil {
				return err
			}
			return errFnFailed
		})
		if !errors.Is(err, errFnFailed) {
			t.Fatalf("got %v, want errFnFailed", err)
		}
		if n := countBrands(t, db, name); n != 0 {
			t.Errorf("brands after rollback: %d, want 0", n)
		}
	})

	t.Run("success commits", func(t *testing.T) {
		err := runner.InTx(ctx, func(tx postgres.DBTX) error {
			return insertBrand(ctx, tx, name)
		})
		if err != nil {
			t.Fatalf("in tx: %v", err)
		}
		if n := countBrands(t, db, name); n != 1 {
			t.Errorf("brands after commit: %d, want 1", n)
		}
	})
}

func TestSavepointRunner(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	runner := postgres.NewSavepointRunner(tx)

	t.Run("error rolls back only fn", func(t *testing.T) {
		// Escrito antes del savepoint: tiene que quedar.
		if err := insertBrand(ctx, tx, "MARCA TEST SP ANTES"); err != nil {
			t.Fatalf("insert before: %v", err)
		}
		err := runner.InTx(ctx, func(tx postgres.DBTX) error {
			if err := insertBrand(ctx, tx, "MARCA TEST SP FALLA"); err != nil {
				return err
			}
			return errFnFailed
		})
		if !errors.Is(err, errFnFailed) {
			t.Fatalf("got %v, want errFnFailed", err)
		}
		if n := countBrands(t, tx, "MARCA TEST SP FALLA"); n != 0 {
			t.Errorf("brand written in fn: %d, want 0", n)
		}
		if n := countBrands(t, tx, "MARCA TEST SP ANTES"); n != 1 {
			t.Errorf("brand written before: %d, want 1", n)
		}
	})

	t.Run("DB error leaves the tx usable", func(t *testing.T) {
		// Un nombre repetido aborta la transacción en Postgres. El ROLLBACK
		// TO SAVEPOINT la recupera: el count de abajo tiene que andar.
		err := runner.InTx(ctx, func(tx postgres.DBTX) error {
			return insertBrand(ctx, tx, "MARCA TEST SP ANTES")
		})
		if err == nil {
			t.Fatal("duplicate brand: got nil error")
		}
		if n := countBrands(t, tx, "MARCA TEST SP ANTES"); n != 1 {
			t.Errorf("brands: %d, want 1", n)
		}
	})

	t.Run("success keeps fn", func(t *testing.T) {
		err := runner.InTx(ctx, func(tx postgres.DBTX) error {
			return insertBrand(ctx, tx, "MARCA TEST SP OK")
		})
		if err != nil {
			t.Fatalf("in tx: %v", err)
		}
		if n := countBrands(t, tx, "MARCA TEST SP OK"); n != 1 {
			t.Errorf("brands: %d, want 1", n)
		}
	})
}
