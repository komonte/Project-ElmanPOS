package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
)

const (
	errCodeUniqueViolation     = "23505"
	errCodeForeignKeyViolation = "23503"
	errCodeCheckViolation      = "23514"
)

// DBTX es lo que un store necesita para hablar con la DB. Lo cumplen tanto
// *sql.DB (la app) como *sql.Tx (una transacción). Así el mismo store sirve
// adentro de una transacción: los tests la deshacen al terminar, y más
// adelante una venta (F-07) podrá usar varios stores en un solo BEGIN/COMMIT.
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type DB struct {
	*sql.DB
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == errCodeUniqueViolation
	}
	return false
}

func isForeignKeyViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == errCodeForeignKeyViolation
	}
	return false
}

func isCheckViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == errCodeCheckViolation
	}
	return false
}
