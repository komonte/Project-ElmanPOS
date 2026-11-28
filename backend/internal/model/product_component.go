package model

import (
	"errors"

	"github.com/shopspring/decimal"
)

// Combos (F-05, 2026-11-28). Un combo (SaleTypeComposite) no tiene stock
// propio: un atado son 20 cigarrillos sueltos, una bolsa son 10 caramelos.
// Solo el suelto tiene stock, y cada uno tiene su precio. Vender el combo
// descuenta sus componentes (usecase/sales).
//   - Un componente es un producto activo por unidad o por peso: ni otro
//     combo (no se anidan) ni un artículo sin registrar (no tiene stock).
//   - Por unidad, la cantidad es entera; por peso, kg con hasta 3 decimales.
//   - Cada componente va una vez por combo: agregarlo de nuevo cambia su
//     cantidad.
//   - Cambiar la receta solo afecta las ventas que vienen: cada venta guarda
//     lo que salió de cada componente (SaleItemComponent).

var (
	ErrNilComponent           = errors.New("component cannot be nil")
	ErrComponentPack          = errors.New("component pack id must be positive")
	ErrComponentProduct       = errors.New("component product id must be positive")
	ErrComponentSelf          = errors.New("a combo cannot be its own component")
	ErrInvalidComponentQty    = errors.New("component quantity must be greater than 0, up to 999999999.999, with up to 3 decimals")
	ErrComponentPackNotCombo  = errors.New("only a combo has components")
	ErrComponentIsCombo       = errors.New("a combo cannot be a component of another combo")
	ErrComponentOpenPrice     = errors.New("an unregistered item cannot be a component")
	ErrComponentInactive      = errors.New("component product is inactive")
	ErrComboWithoutComponents = errors.New("the combo has no components")
)

// ProductComponent es un renglón de la receta de un combo (02 4.6): cuánto
// de ComponentID lleva una unidad de PackID.
type ProductComponent struct {
	ID          int64           `json:"id" db:"id"`
	PackID      int64           `json:"pack_id" db:"pack_id"`
	ComponentID int64           `json:"component_id" db:"component_id"`
	Quantity    decimal.Decimal `json:"quantity" db:"quantity"`
	CreatedAt   string          `json:"created_at" db:"created_at"`

	// Solo lectura: el store los llena con un JOIN.
	ComponentSKU      string   `json:"component_sku,omitempty" db:"-"`
	ComponentName     string   `json:"component_name,omitempty" db:"-"`
	ComponentSaleType SaleType `json:"component_sale_type,omitempty" db:"-"`
	ComponentActive   bool     `json:"component_active" db:"-"`
}

// Validate revisa lo que escribe el usuario. Que los productos existan y
// sirvan (CheckComponent) lo revisa el caso de uso con la DB.
func (c *ProductComponent) Validate() error {
	if c == nil {
		return ErrNilComponent
	}
	if c.PackID <= 0 {
		return ErrComponentPack
	}
	if c.ComponentID <= 0 {
		return ErrComponentProduct
	}
	if c.PackID == c.ComponentID {
		return ErrComponentSelf
	}
	if !c.Quantity.IsPositive() || !validQuantity(c.Quantity) {
		return ErrInvalidComponentQty
	}
	return nil
}

// CheckComponent dice si comp puede ir, con la cantidad q, en el combo pack.
func CheckComponent(pack, comp *Product, q decimal.Decimal) error {
	switch {
	case pack.SaleType != SaleTypeComposite:
		return ErrComponentPackNotCombo
	case comp.SaleType == SaleTypeComposite:
		return ErrComponentIsCombo
	case comp.OpenPrice:
		return ErrComponentOpenPrice
	case !comp.IsActive:
		return ErrComponentInactive
	}
	return comp.CheckWholeQuantity(q)
}

// SaleItemComponent es lo que salió de un componente para un renglón de
// combo (migración 0023): Quantity es la cantidad del renglón por la de la
// receta. Uncovered es lo que el sistema no tenía (modo arranque): no salió
// del stock y queda para contar.
type SaleItemComponent struct {
	ID         int64           `json:"id" db:"id"`
	SaleItemID int64           `json:"sale_item_id" db:"sale_item_id"`
	ProductID  int64           `json:"product_id" db:"product_id"`
	Quantity   decimal.Decimal `json:"quantity" db:"quantity"`
	UnitCost   decimal.Decimal `json:"unit_cost" db:"unit_cost"`
	Uncovered  decimal.Decimal `json:"uncovered_quantity" db:"uncovered_quantity"`
}
