package model

import (
	"errors"

	"github.com/shopspring/decimal"
)

var (
	ErrNilPrice     = errors.New("price cannot be nil")
	ErrInvalidPrice = errors.New("price must be greater than 0, up to 9999999999.99, with up to 2 decimals")
)

// NUMERIC(12,2): 10 dígitos enteros y 2 decimales.
var maxPrice = decimal.RequireFromString("9999999999.99")

// Price es el precio de venta (PVP) de un producto desde un momento (02 4.7,
// F-03). Un cambio de precio no edita la fila anterior: agrega una nueva, así
// queda la historia. El precio vigente es el de valid_from más reciente que ya
// empezó.
type Price struct {
	ID        int64           `json:"id" db:"id"`
	ProductID int64           `json:"product_id" db:"product_id"`
	UnitPrice decimal.Decimal `json:"unit_price" db:"unit_price"`

	// Solo lectura: el store lo pone en now() al guardar. Por ahora un precio
	// rige siempre desde que se carga; programarlo para más adelante queda
	// para después.
	ValidFrom string `json:"valid_from" db:"valid_from"`

	CreatedAt string `json:"created_at" db:"created_at"`
}

// Validate revisa el precio que escribe el usuario. No revisa ProductID: eso
// lo resuelve el caso de uso (el producto tiene que existir y estar activo).
func (p *Price) Validate() error {
	if p == nil {
		return ErrNilPrice
	}
	// Más de 2 decimales: la DB redondearía en silencio; mejor avisar que
	// guardar otro número.
	if !p.UnitPrice.IsPositive() || p.UnitPrice.GreaterThan(maxPrice) ||
		!p.UnitPrice.Equal(p.UnitPrice.Round(2)) {
		return ErrInvalidPrice
	}
	return nil
}
