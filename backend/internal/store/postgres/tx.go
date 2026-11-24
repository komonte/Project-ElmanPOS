package postgres

import (
	"context"
	"database/sql"
	"errors"
)

// TxRunner corre fn dentro de una transacción: si fn devuelve error, se
// deshace todo lo que hizo; si no, se guarda. fn recibe un DBTX para armar
// los stores que necesite (NewProductStore(tx), NewProductCodeStore(tx)...),
// así varias escrituras quedan en un solo BEGIN...COMMIT (D-02, BR-04).
//
// Es una interfaz para que un caso de uso no dependa de *sql.DB: en la app se
// usa DBTxRunner, y en los tests SavepointRunner dentro de la transacción
// del test.
type TxRunner interface {
	InTx(ctx context.Context, fn func(tx DBTX) error) error
}

// DBTxRunner abre una transacción de verdad en la DB.
type DBTxRunner struct {
	db *sql.DB
}

func NewTxRunner(db *sql.DB) *DBTxRunner {
	return &DBTxRunner{db: db}
}

func (r *DBTxRunner) InTx(ctx context.Context, fn func(tx DBTX) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	// Si fn falla (o entra en pánico) se deshace. Después de un Commit que
	// salió bien, Rollback no hace nada (devuelve sql.ErrTxDone).
	defer tx.Rollback()

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// SavepointRunner corre fn dentro de una transacción que ya está abierta,
// con un SAVEPOINT: si fn falla, ROLLBACK TO SAVEPOINT deshace solo lo que
// hizo fn. Lo usan los tests, que trabajan dentro de una transacción que se
// deshace al final (testTx) y no pueden abrir otra adentro.
//
// ROLLBACK TO SAVEPOINT también saca a la transacción del estado "abortada"
// en que la deja un error de Postgres: después se puede seguir consultando.
type SavepointRunner struct {
	tx *sql.Tx
}

func NewSavepointRunner(tx *sql.Tx) *SavepointRunner {
	return &SavepointRunner{tx: tx}
}

func (r *SavepointRunner) InTx(ctx context.Context, fn func(tx DBTX) error) error {
	if _, err := r.tx.ExecContext(ctx, `SAVEPOINT intx;`); err != nil {
		return err
	}
	if err := fn(r.tx); err != nil {
		if _, rbErr := r.tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT intx;`); rbErr != nil {
			return errors.Join(err, rbErr)
		}
		return err
	}
	_, err := r.tx.ExecContext(ctx, `RELEASE SAVEPOINT intx;`)
	return err
}

// Fallan al compilar si alguno deja de cumplir TxRunner.
var (
	_ TxRunner = (*DBTxRunner)(nil)
	_ TxRunner = (*SavepointRunner)(nil)
)
