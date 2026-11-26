package postgres

import (
	"context"
	"time"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

// PurchasePriceStore lee las compras recibidas y el estado de los precios
// de sus productos (precios por factura, F-06). Solo lee: los precios se
// guardan con PriceStore (prices.Set).
type PurchasePriceStore struct {
	db DBTX
}

func NewPurchasePriceStore(db DBTX) *PurchasePriceStore {
	return &PurchasePriceStore{db: db}
}

// currentPrice es el precio vigente de p.id (como PriceStore.Current) para
// un LEFT JOIN LATERAL: unit_price y valid_from, o nada si no tiene.
const currentPrice = `LEFT JOIN LATERAL (
			SELECT unit_price, valid_from FROM prices WHERE product_id = p.id
			ORDER BY valid_from DESC LIMIT 1
		) pr ON TRUE`

// lastCost es el costo vigente de p.id (como CostStore.Current).
const lastCost = `LEFT JOIN LATERAL (
			SELECT unit_cost, avg_cost FROM product_costs WHERE product_id = p.id
			ORDER BY valid_from DESC LIMIT 1
		) c ON TRUE`

// summaries es cada compra con sus productos distintos y cuántos de los
// activos no están cotizados (sin precio, o con un precio anterior a la
// compra).
const summaries = `
	SELECT pu.id, pu.received_at, s.name, COALESCE(pu.voucher_number, ''), u.name, pu.total,
	       (SELECT count(DISTINCT pi.product_id) FROM purchase_items pi WHERE pi.purchase_id = pu.id) AS products,
	       (SELECT count(DISTINCT pi.product_id)
	        FROM purchase_items pi
	        JOIN products p ON p.id = pi.product_id
	        ` + currentPrice + `
	        WHERE pi.purchase_id = pu.id AND p.is_active
	          AND (pr.valid_from IS NULL OR pr.valid_from < pu.received_at)) AS unpriced
	FROM purchases pu
	JOIN suppliers s ON s.id = pu.supplier_id
	LEFT JOIN users u ON u.id = pu.received_by`

// List devuelve las últimas limit compras, la más nueva primero. Con
// onlyUnpriced, solo las que tienen productos sin cotizar.
func (s *PurchasePriceStore) List(ctx context.Context, onlyUnpriced bool, limit int) ([]model.PurchaseSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT * FROM (`+summaries+`) x
		WHERE NOT $1 OR x.unpriced > 0
		ORDER BY x.received_at DESC, x.id DESC
		LIMIT $2;`, onlyUnpriced, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.PurchaseSummary
	for rows.Next() {
		var p model.PurchaseSummary
		if err := rows.Scan(&p.ID, &p.ReceivedAt, &p.SupplierName, &p.VoucherNumber, &p.ReceivedByName, &p.Total,
			&p.Products, &p.Unpriced); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CountUnpriced dice cuántas compras tienen productos sin cotizar.
func (s *PurchasePriceStore) CountUnpriced(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM (`+summaries+`) x WHERE x.unpriced > 0;`).Scan(&n)
	return n, err
}

// Products devuelve los productos de una compra, en el orden de sus
// renglones: lo recibido (sumando renglones), el costo de la factura (el
// del último renglón del producto), el costo promedio y el precio vigente.
// Vacía si la compra no existe.
func (s *PurchasePriceStore) Products(ctx context.Context, purchaseID int64) ([]model.PricedProduct, error) {
	return s.priced(ctx, `
		WITH lines AS (
			SELECT pi.product_id, MIN(pi.id) AS first, SUM(pi.quantity) AS qty,
			       (array_agg(pi.unit_cost ORDER BY pi.id DESC))[1] AS cost
			FROM purchase_items pi WHERE pi.purchase_id = $1
			GROUP BY pi.product_id
		)
		SELECT p.id, p.sku, p.name, p.is_active, p.sale_type, p.margin_percent, p.category_id,
		       l.qty, l.cost, COALESCE(c.avg_cost, 0), pr.unit_price, pr.valid_from
		FROM lines l
		JOIN products p ON p.id = l.product_id
		`+lastCost+`
		`+currentPrice+`
		ORDER BY l.first;`, purchaseID)
}

// missing son los productos activos, que no son combos, con stock y sin
// precio: no se pueden vender.
const missing = `
		WITH bal AS (SELECT product_id, SUM(quantity) AS q FROM stock_balances GROUP BY product_id)
		SELECT p.id, p.sku, p.name, p.is_active, p.sale_type, p.margin_percent, p.category_id,
		       bal.q, COALESCE(c.unit_cost, 0), COALESCE(c.avg_cost, 0), NULL::numeric, NULL::timestamptz
		FROM products p
		JOIN bal ON bal.product_id = p.id AND bal.q > 0
		` + lastCost + `
		WHERE p.is_active AND p.sale_type <> 'COMPOSITE'
		  AND NOT EXISTS (SELECT 1 FROM prices WHERE product_id = p.id)`

// MissingPrices devuelve los productos con stock y sin precio, por nombre.
func (s *PurchasePriceStore) MissingPrices(ctx context.Context) ([]model.PricedProduct, error) {
	return s.priced(ctx, missing+` ORDER BY p.name, p.id;`)
}

// CountMissingPrices dice cuántos productos tienen stock y no tienen precio.
func (s *PurchasePriceStore) CountMissingPrices(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM (`+missing+`) x;`).Scan(&n)
	return n, err
}

func (s *PurchasePriceStore) priced(ctx context.Context, q string, args ...any) ([]model.PricedProduct, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.PricedProduct
	for rows.Next() {
		var p model.PricedProduct
		var from *time.Time
		if err := rows.Scan(&p.ProductID, &p.SKU, &p.Name, &p.Active, &p.SaleType, &p.MarginPercent, &p.CategoryID,
			&p.Quantity, &p.UnitCost, &p.AvgCost, &p.Price, &from); err != nil {
			return nil, err
		}
		p.PriceFrom = from
		out = append(out, p)
	}
	return out, rows.Err()
}
