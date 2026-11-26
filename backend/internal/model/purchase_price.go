package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// Precios por factura (F-06, 2026-11-26). Después de recibir una compra, el
// admin revisa la factura y pone los precios de venta. Cada producto de la
// factura tiene uno de tres estados:
//   - Sin precio (PriceMissing): no se puede vender.
//   - Precio anterior a esta factura (PriceOld): tiene precio, pero se puso
//     antes de recibir esta mercadería; si el costo subió, puede estar
//     desactualizado.
//   - Cotizado (PriceOK): tiene un precio puesto al recibir la factura o
//     después.
//
// Una factura está "sin cotizar" mientras alguno de sus productos activos no
// esté cotizado. Los desactivados no cuentan: no se venden ni se les pone
// precio.

// PriceState es el estado del precio de un producto respecto de una factura.
type PriceState string

const (
	PriceMissing PriceState = "MISSING"
	PriceOld     PriceState = "OLD"
	PriceOK      PriceState = "OK"
)

// PriceStateAt dice el estado de un precio vigente desde priceFrom (nil:
// no tiene precio) para una factura recibida en receivedAt.
func PriceStateAt(priceFrom *time.Time, receivedAt time.Time) PriceState {
	switch {
	case priceFrom == nil:
		return PriceMissing
	case priceFrom.Before(receivedAt):
		return PriceOld
	default:
		return PriceOK
	}
}

// PricedProduct es un producto para ponerle precio: de una factura (con lo
// que se recibió) o de la lista de "con stock y sin precio".
type PricedProduct struct {
	ProductID     int64           `json:"product_id"`
	SKU           string          `json:"sku"`
	Name          string          `json:"name"`
	Active        bool            `json:"active"`
	SaleType      SaleType        `json:"sale_type"`
	MarginPercent decimal.Decimal `json:"margin_percent"`
	CategoryID    int64           `json:"category_id"`
	// Quantity es lo recibido en la factura (sumando sus renglones), o el
	// stock en la lista de sin precio.
	Quantity decimal.Decimal `json:"quantity"`
	// UnitCost es el costo de la factura (el del último renglón del
	// producto), o el último costo en la lista de sin precio (0: no tiene).
	UnitCost decimal.Decimal `json:"unit_cost"`
	AvgCost  decimal.Decimal `json:"avg_cost"` // 0: no tiene
	// Price es el precio vigente y PriceFrom desde cuándo (nil: sin precio).
	Price     *decimal.Decimal `json:"price"`
	PriceFrom *time.Time       `json:"price_from"`
}

// PurchaseSummary es una factura en la lista de compras recibidas.
type PurchaseSummary struct {
	ID             int64           `json:"id"`
	ReceivedAt     string          `json:"received_at"`
	SupplierName   string          `json:"supplier_name"`
	VoucherNumber  string          `json:"voucher_number"`
	ReceivedByName *string         `json:"received_by_name"`
	Total          decimal.Decimal `json:"total"`
	Products       int             `json:"products"` // productos distintos
	// Unpriced son los productos activos que no están cotizados (sin precio
	// o con precio anterior a la factura).
	Unpriced int `json:"unpriced"`
}
