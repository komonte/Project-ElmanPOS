package model

import (
	"errors"

	"github.com/shopspring/decimal"
)

var (
	ErrNilCost     = errors.New("cost cannot be nil")
	ErrInvalidCost = errors.New("cost must be greater than 0, up to 9999999999.99, with up to 2 decimals")
)

// NUMERIC(12,2): 10 dígitos enteros y 2 decimales, igual que el precio.
var maxCost = decimal.RequireFromString("9999999999.99")

var hundred = decimal.NewFromInt(100)

// Cost es el costo de un producto desde un momento (02 4.7, F-04). Igual que
// el precio, un cambio agrega una fila nueva y no edita la anterior.
//
//   - UnitCost: lo que costó la última vez (el costo de reposición). Con este
//     se sugiere el precio de venta.
//   - AvgCost: el costo promedio ponderado de lo que hay en stock. Sirve para
//     valuar el inventario y calcular el margen real; no lo escribe el
//     usuario, lo calcula AverageCost.
type Cost struct {
	ID        int64           `json:"id" db:"id"`
	ProductID int64           `json:"product_id" db:"product_id"`
	UnitCost  decimal.Decimal `json:"unit_cost" db:"unit_cost"`
	AvgCost   decimal.Decimal `json:"avg_cost" db:"avg_cost"`

	// Solo lectura: el store lo pone al guardar.
	ValidFrom string `json:"valid_from" db:"valid_from"`

	CreatedAt string `json:"created_at" db:"created_at"`
}

// Validate revisa el costo que escribe el usuario. No revisa AvgCost (lo
// calcula el sistema) ni ProductID (lo resuelve el caso de uso).
func (c *Cost) Validate() error {
	if c == nil {
		return ErrNilCost
	}
	// Más de 2 decimales: la DB redondearía en silencio.
	if !c.UnitCost.IsPositive() || c.UnitCost.GreaterThan(maxCost) ||
		!c.UnitCost.Equal(c.UnitCost.Round(2)) {
		return ErrInvalidCost
	}
	return nil
}

// AverageCost calcula el costo promedio nuevo cuando llega un costo (F-04):
//
//	nuevo = (prom_anterior × stock + costo × cantidad) / (stock + cantidad)
//
// Parámetros:
//   - prev: el costo vigente, o nil si el producto nunca tuvo costo.
//   - onHand: el stock actual, antes de sumar lo que entra.
//   - unitCost: el costo que llega.
//   - qtyIn: lo que entra. 0 en una carga manual (no entra mercadería).
//
// Reglas:
//   - Sin costo previo, o sin stock: el promedio es el costo que llega (no hay
//     nada con qué promediar).
//   - Carga manual (qtyIn <= 0) con stock: el promedio no cambia. Solo una
//     compra (F-06) lo mueve, porque solo ahí entra mercadería a ese costo.
//   - Si no: la fórmula, redondeada a 2 decimales como la guarda la DB.
func AverageCost(prev *Cost, onHand, unitCost, qtyIn decimal.Decimal) decimal.Decimal {
	if prev == nil || !onHand.IsPositive() {
		return unitCost
	}
	if !qtyIn.IsPositive() {
		return prev.AvgCost
	}
	total := prev.AvgCost.Mul(onHand).Add(unitCost.Mul(qtyIn))
	return total.Div(onHand.Add(qtyIn)).Round(2)
}

// SuggestPrice sugiere el precio de venta para un costo, un margen y una tasa
// de IVA (F-04):
//
//	sugerido = costo / (1 - margen/100) × (1 + iva/100)
//
// El costo se carga sin IVA y el precio de venta lo incluye (es el precio al
// público), así que la sugerencia suma el IVA de la categoría del producto.
// El margen es sobre el precio sin IVA: con 30%, de cada $100 sin IVA que se
// cobran, $30 quedan de ganancia. Costo 700, margen 30%, IVA 21% → 1000 sin
// IVA → sugerido 1210.
//
// La cuenta se hace entera y se redondea una sola vez, a 2 decimales.
//
// Devuelve false si no hay sugerencia: margen 0 (el producto se pone a mano),
// margen fuera de rango (>= 100 dividiría por cero) o IVA negativo. La
// sugerencia nunca se guarda sola: el usuario la acepta como precio nuevo
// (F-03) o la ignora.
func SuggestPrice(unitCost, marginPercent, vatRate decimal.Decimal) (decimal.Decimal, bool) {
	if !marginPercent.IsPositive() || !marginPercent.LessThan(hundred) || vatRate.IsNegative() {
		return decimal.Zero, false
	}
	one := decimal.NewFromInt(1)
	share := one.Sub(marginPercent.Div(hundred))
	withVAT := unitCost.Mul(one.Add(vatRate.Div(hundred)))
	// Primero multiplica y después divide: así hay una sola división.
	return withVAT.Div(share).Round(2), true
}
