package model

import (
	"errors"

	"github.com/shopspring/decimal"
)

var ErrNilStockMovement = errors.New("stock movement cannot be nil")

// Tipos de movimiento (ENUM movement_type, 02 3). Por ahora solo se usa el de
// compra; los demás llegan con ventas, devoluciones y conteos.
const MovementPurchase = "PURCHASE"

// StockMovement es una fila del libro de stock (02 4.12): cada entrada o
// salida de mercadería, con su costo y de dónde viene. Solo se agregan filas:
// un trigger rechaza UPDATE y DELETE (BR-05). El saldo (stock_balances) es
// el resumen de estas filas.
//
// No tiene Validate: lo arma el caso de uso con datos ya validados. La DB
// revisa el signo según el tipo y que tenga exactamente un origen (compra,
// venta o conteo).
type StockMovement struct {
	ID           int64           `json:"id" db:"id"`
	ProductID    int64           `json:"product_id" db:"product_id"`
	StoreID      int64           `json:"store_id" db:"store_id"`
	BatchID      *int64          `json:"batch_id" db:"batch_id"`
	MovementType string          `json:"movement_type" db:"movement_type"`
	Quantity     decimal.Decimal `json:"quantity" db:"quantity"` // positiva si entra
	UnitCost     decimal.Decimal `json:"unit_cost" db:"unit_cost"`
	PurchaseID   *int64          `json:"purchase_id" db:"purchase_id"`
	// PerformedBy queda en nil hasta que haya login (F-13, migración 0013).
	PerformedBy *int64 `json:"performed_by" db:"performed_by"`
	CreatedAt   string `json:"created_at" db:"created_at"`
}
