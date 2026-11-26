package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
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
// No bloquea las filas. Para calcular el costo promedio se usa LockOnHand.
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

// OnHandAt devuelve cuánto hay de un producto en un local: la foto que guarda
// un conteo (F-09) cuando se carga el producto. 0 si no tiene saldo ahí. No
// bloquea.
func (s *StockBalanceStore) OnHandAt(ctx context.Context, productID, storeID int64) (decimal.Decimal, error) {
	var qty decimal.Decimal
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(quantity), 0)
		FROM stock_balances
		WHERE product_id = $1 AND store_id = $2;`, productID, storeID).Scan(&qty)
	if err != nil {
		return decimal.Zero, err
	}
	return qty, nil
}

// LockOnHand es OnHand bloqueando, para calcular el costo promedio dentro de
// una transacción. Hasta que esa transacción termine:
//   - Otra que también llame a LockOnHand para el mismo producto espera. Así
//     dos compras a la vez no promedian contra el mismo stock viejo, y la
//     segunda lee el costo y el stock que dejó la primera.
//   - Una venta o ajuste que quiera cambiar el saldo del producto espera.
//
// Bloquea la fila del producto (FOR NO KEY UPDATE) además de los saldos: si
// el producto todavía no tiene saldo no hay filas que bloquear, y dos
// primeras compras leerían 0 las dos. FOR NO KEY UPDATE no frena a quien solo
// referencia el producto (una venta que inserta sale_items), solo a quien lo
// bloquea igual o lo edita.
//
// ErrNotFound si el producto no existe.
func (s *StockBalanceStore) LockOnHand(ctx context.Context, productID int64) (decimal.Decimal, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM products WHERE id = $1 FOR NO KEY UPDATE;`, productID).Scan(&id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return decimal.Zero, store.ErrNotFound
		}
		return decimal.Zero, err
	}

	// FOR UPDATE no se puede usar con SUM: se leen las filas y se suman acá.
	rows, err := s.db.QueryContext(ctx, `
		SELECT quantity FROM stock_balances
		WHERE product_id = $1
		FOR UPDATE;`, productID)
	if err != nil {
		return decimal.Zero, err
	}
	defer rows.Close()

	total := decimal.Zero
	for rows.Next() {
		var qty decimal.Decimal
		if err := rows.Scan(&qty); err != nil {
			return decimal.Zero, err
		}
		total = total.Add(qty)
	}
	if err := rows.Err(); err != nil {
		return decimal.Zero, err
	}
	return total, nil
}

// Add suma qty al saldo del producto en un local y devuelve el saldo nuevo.
// Si todavía no hay fila (primera vez que entra stock ahí), la crea. Solo
// para sumar: las salidas (ventas, mermas) van a tener su propio método, que
// revise que no quede negativo.
//   - ErrInvalidReference: el producto o el local no existen.
func (s *StockBalanceStore) Add(ctx context.Context, productID, storeID int64, qty decimal.Decimal) (decimal.Decimal, error) {
	var balance decimal.Decimal
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO stock_balances (product_id, store_id, quantity)
		VALUES ($1, $2, $3)
		ON CONFLICT (product_id, store_id)
		DO UPDATE SET quantity = stock_balances.quantity + EXCLUDED.quantity
		RETURNING quantity;`, productID, storeID, qty).Scan(&balance)
	if err != nil {
		if isForeignKeyViolation(err) {
			return decimal.Zero, store.ErrInvalidReference
		}
		return decimal.Zero, err
	}
	return balance, nil
}
