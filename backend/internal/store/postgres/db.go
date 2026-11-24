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

// listPage arma una página de ListAll con dos consultas: countQ cuenta las
// filas que pasan el filtro y rowsQ trae las primeras limit, ya ordenadas.
// Las dos reciben los argumentos del filtro (filter) desde $1 (la búsqueda,
// vacía = sin filtro; productos agrega el código de barras en $2). rowsQ
// recibe además el límite en el lugar siguiente: $2 con un argumento, $3 con dos.
//
// Son dos consultas y no una con COUNT(*) OVER (): con la ventana, Postgres
// tiene que leer y ordenar todas las filas antes de cortar; separadas, la de
// filas puede usar un índice ordenado y frenar a las limit primeras.
func listPage[T any](ctx context.Context, db DBTX, countQ, rowsQ string,
	scan func(rowScanner) (*T, error), limit int, filter ...any) ([]*T, int, error) {
	var total int
	if err := db.QueryRowContext(ctx, countQ, filter...).Scan(&total); err != nil {
		return nil, 0, err
	}

	list := make([]*T, 0)
	if limit <= 0 || total == 0 {
		return list, total, nil
	}
	rows, err := db.QueryContext(ctx, rowsQ, append(filter, limit)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	for rows.Next() {
		x, err := scan(rows)
		if err != nil {
			return nil, 0, err
		}
		list = append(list, x)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
