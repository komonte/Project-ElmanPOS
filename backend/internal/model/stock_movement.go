package model

import (
	"errors"

	"github.com/shopspring/decimal"
)

var ErrNilStockMovement = errors.New("stock movement cannot be nil")

// Tipos de movimiento (ENUM movement_type, 02 3). Por ahora se usan el de
// compra, el de ajuste (conteos y salidas conocidas) y el de venta; el de
// devolución llega con la anulación (F-08).
const (
	MovementPurchase   = "PURCHASE"
	MovementAdjustment = "ADJUSTMENT"
	MovementSale       = "SALE"
)

// Motivos de un ajuste (ENUM adjustment_reason). ReasonCount es el del
// conteo: la diferencia entre lo contado y lo que decía el sistema. Los de
// las salidas conocidas están en stock_adjustment.go.
const ReasonCount = "COUNT"

// StockMovement es una fila del libro de stock (02 4.12): cada entrada o
// salida de mercadería, con su costo y de dónde viene. Solo se agregan filas:
// un trigger rechaza UPDATE y DELETE (BR-05). El saldo (stock_balances) es
// el resumen de estas filas.
//
// No tiene Validate: lo arma el caso de uso con datos ya validados. La DB
// revisa el signo según el tipo y que tenga exactamente un origen (compra,
// venta, conteo o salida conocida).
type StockMovement struct {
	ID           int64  `json:"id" db:"id"`
	ProductID    int64  `json:"product_id" db:"product_id"`
	StoreID      int64  `json:"store_id" db:"store_id"`
	BatchID      *int64 `json:"batch_id" db:"batch_id"`
	MovementType string `json:"movement_type" db:"movement_type"`
	// Reason solo va en los ajustes (ADJUSTMENT); vacío se guarda NULL.
	Reason     string          `json:"reason,omitempty" db:"reason"`
	Quantity   decimal.Decimal `json:"quantity" db:"quantity"` // positiva si entra
	UnitCost   decimal.Decimal `json:"unit_cost" db:"unit_cost"`
	SaleID     *int64          `json:"sale_id" db:"sale_id"`
	PurchaseID *int64          `json:"purchase_id" db:"purchase_id"`
	CountID    *int64          `json:"count_id" db:"count_id"`
	// AdjustmentID: la salida conocida que lo originó (F-09b).
	AdjustmentID *int64 `json:"adjustment_id" db:"adjustment_id"`
	// PerformedBy queda en nil hasta que haya login (F-13, migración 0013).
	PerformedBy *int64 `json:"performed_by" db:"performed_by"`
	CreatedAt   string `json:"created_at" db:"created_at"`
}
