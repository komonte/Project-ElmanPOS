// Package usecasetest tiene ayudas que comparten los tests de los casos de
// uso. No lo usa el programa.
package usecasetest

import (
	"context"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// WantAuthor revisa quién hizo algo (F-13). q devuelve dos números: cuántas
// filas tienen al autor esperado y cuántas tienen otro o NULL. Tiene que
// haber al menos una con el autor (si no, el control no revisaría nada) y
// ninguna sin él. Por ejemplo:
//
//	SELECT count(*) FILTER (WHERE performed_by = $1),
//	       count(*) FILTER (WHERE performed_by IS DISTINCT FROM $1)
//	FROM stock_movements WHERE sale_id = $2
func WantAuthor(t testing.TB, ctx context.Context, db postgres.DBTX, what, q string, args ...any) {
	t.Helper()
	var with, without int
	if err := db.QueryRowContext(ctx, q, args...).Scan(&with, &without); err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if with == 0 || without != 0 {
		t.Errorf("%s: %d rows by the user, %d by someone else or nobody", what, with, without)
	}
}

// Author arma la consulta de WantAuthor para una columna de una tabla:
// Author("stock_movements", "performed_by", "sale_id = $2").
func Author(table, column, where string) string {
	return `SELECT count(*) FILTER (WHERE ` + column + ` = $1),
	               count(*) FILTER (WHERE ` + column + ` IS DISTINCT FROM $1)
	        FROM ` + table + ` WHERE ` + where
}
