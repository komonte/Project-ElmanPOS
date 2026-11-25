package model

import (
	"errors"

	"github.com/shopspring/decimal"
)

var ErrNilBatch = errors.New("batch cannot be nil")

// ProductBatch es un lote de un producto que controla lotes (02 4.11, P-04):
// cuánto entró, cuánto queda, a qué costo y cuándo vence. Lo crea la
// recepción de compras (F-06); las ventas descuentan RemainingQuantity del
// lote más viejo (FIFO, F-07).
//
// No tiene Validate: sus datos salen de un PurchaseItem ya validado.
type ProductBatch struct {
	ID                int64           `json:"id" db:"id"`
	ProductID         int64           `json:"product_id" db:"product_id"`
	BatchNumber       string          `json:"batch_number" db:"batch_number"`
	ExpiryDate        string          `json:"expiry_date" db:"expiry_date"` // "2027-03-31"; vacío si no vence
	OriginalQuantity  decimal.Decimal `json:"original_quantity" db:"original_quantity"`
	RemainingQuantity decimal.Decimal `json:"remaining_quantity" db:"remaining_quantity"`
	UnitCost          decimal.Decimal `json:"unit_cost" db:"unit_cost"` // sin IVA, el del renglón
	IsConsumed        bool            `json:"is_consumed" db:"is_consumed"`
	CreatedAt         string          `json:"created_at" db:"created_at"`
	UpdatedAt         string          `json:"updated_at" db:"updated_at"`
}
