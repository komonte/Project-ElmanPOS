package postgres

import (
	"context"

	"github.com/shopspring/decimal"
)

// StockBalanceStore lee los saldos de stock (02 4.13): cuánto hay de cada
// producto en cada local. El saldo es un resumen de los movimientos
// (stock_movements); lo actualizan las ventas, compras y conteos dentro de
// su transacción, nunca a mano.
type StockBalanceStore struct {
	db DBTX
}

// NewStockBalanceStore recibe un DBTX: el *sql.DB, o la transacción del caso
// de uso que necesita el stock para hacer una cuenta (el costo promedio).
func NewStockBalanceStore(db DBTX) *StockBalanceStore {
	return &StockBalanceStore{db: db}
}

// OnHand devuelve cuánto hay de un producto sumando todos los locales. El
// costo promedio es por producto, no por local, así que promedia contra todo
// el stock. Devuelve 0 si el producto no tiene saldo (nunca entró stock) o
// si no existe.
//
// No bloquea las filas: alcanza para la carga manual de costo. La recepción
// de compras (F-06) va a necesitar bloquear el saldo antes de leerlo, para
// que dos compras a la vez no calculen el promedio con el mismo stock viejo.
func (s *StockBalanceStore) OnHand(ctx context.Context, productID int64) (decimal.Decimal, error) {
	var qty decimal.Decimal
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(quantity), 0)
		FROM stock_balances
		WHERE product_id = $1;`, productID).Scan(&qty)
	if err != nil {
		return decimal.Zero, err
	}
	return qty, nil
}
