package purchases

// Precios por factura (F-06, 2026-11-26): la lista de compras recibidas con
// su estado de precios, el detalle de una factura con lo que hace falta para
// ponerle precio a cada producto, y los productos con stock y sin precio
// (los que no vienen de una factura: un conteo, una carga a mano). Solo lee:
// los precios se guardan con prices.Set / prices.SetMany.

import (
	"context"
	"errors"
	"time"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/costs"
)

// ListLimit es cuántas compras muestra la lista (las últimas).
const ListLimit = 100

// List devuelve las últimas ListLimit compras, la más nueva primero. Con
// onlyUnpriced, solo las que tienen productos sin cotizar.
func (s *Service) List(ctx context.Context, onlyUnpriced bool) ([]model.PurchaseSummary, error) {
	return postgres.NewPurchasePriceStore(s.db).List(ctx, onlyUnpriced, ListLimit)
}

// PricedRow es un producto para ponerle precio, con su estado y el precio
// sugerido (con el margen del producto y el IVA de su categoría, sobre el
// costo de la factura; nil sin margen o sin costo).
type PricedRow struct {
	model.PricedProduct
	State     model.PriceState `json:"state"`
	Suggested *decimal.Decimal `json:"suggested"`
}

// Invoice es el detalle de una factura para cotizarla.
type Invoice struct {
	Summary  *model.PurchaseSummary `json:"summary"`
	Products []PricedRow            `json:"products"`
}

// Invoice devuelve una factura con sus productos y el estado de cada precio.
//   - service.ErrInvalidID: id <= 0.
//   - service.ErrNotFound: no existe.
func (s *Service) Invoice(ctx context.Context, id int64) (*Invoice, error) {
	if id <= 0 {
		return nil, service.ErrInvalidID
	}
	pp := postgres.NewPurchasePriceStore(s.db)
	sum, err := pp.Summary(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, service.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	received, err := time.Parse(time.RFC3339Nano, sum.ReceivedAt)
	if err != nil {
		return nil, err
	}
	products, err := pp.Products(ctx, id)
	if err != nil {
		return nil, err
	}
	rows, err := s.pricedRows(ctx, products, &received)
	if err != nil {
		return nil, err
	}
	return &Invoice{Summary: sum, Products: rows}, nil
}

// MissingPrices devuelve los productos con stock y sin precio, con el
// precio sugerido sobre su último costo.
func (s *Service) MissingPrices(ctx context.Context) ([]PricedRow, error) {
	products, err := postgres.NewPurchasePriceStore(s.db).MissingPrices(ctx)
	if err != nil {
		return nil, err
	}
	return s.pricedRows(ctx, products, nil)
}

// Pending es lo que el Panel le avisa al admin.
type Pending struct {
	MissingPrices     int `json:"missing_prices"`     // productos con stock y sin precio
	UnpricedPurchases int `json:"unpriced_purchases"` // facturas sin cotizar
}

// Pending cuenta los productos sin precio y las facturas sin cotizar.
func (s *Service) Pending(ctx context.Context) (*Pending, error) {
	pp := postgres.NewPurchasePriceStore(s.db)
	missing, err := pp.CountMissingPrices(ctx)
	if err != nil {
		return nil, err
	}
	unpriced, err := pp.CountUnpriced(ctx)
	if err != nil {
		return nil, err
	}
	return &Pending{MissingPrices: missing, UnpricedPurchases: unpriced}, nil
}

// pricedRows agrega el estado (respecto de receivedAt; sin factura, solo
// "sin precio" o "cotizado") y el sugerido a cada producto.
func (s *Service) pricedRows(ctx context.Context, products []model.PricedProduct, receivedAt *time.Time) ([]PricedRow, error) {
	rows := make([]PricedRow, len(products))
	for i, p := range products {
		row := PricedRow{PricedProduct: p, State: model.PriceOK}
		switch {
		case p.PriceFrom == nil:
			row.State = model.PriceMissing
		case receivedAt != nil:
			row.State = model.PriceStateAt(p.PriceFrom, *receivedAt)
		}
		if p.UnitCost.IsPositive() {
			prod := &model.Product{ID: p.ProductID, CategoryID: p.CategoryID, MarginPercent: p.MarginPercent}
			sug, err := costs.Suggest(ctx, s.db, prod, p.UnitCost)
			if err != nil {
				return nil, err
			}
			row.Suggested = sug
		}
		rows[i] = row
	}
	return rows, nil
}
