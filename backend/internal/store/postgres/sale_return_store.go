package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// SaleReturnStore guarda las devoluciones (0028). Solo agrega filas; el
// stock y la caja los arma el caso de uso en la misma transacción.
type SaleReturnStore struct {
	db DBTX
}

func NewSaleReturnStore(db DBTX) *SaleReturnStore {
	return &SaleReturnStore{db: db}
}

// Create guarda la devolución con sus renglones y pagos; llena ids y fecha.
func (s *SaleReturnStore) Create(ctx context.Context, r *model.SaleReturn) error {
	if err := s.db.QueryRowContext(ctx, `
		INSERT INTO sale_returns (sale_id, cashier_session_id, reason, total, created_by)
		VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at;`,
		r.SaleID, r.CashierSessionID, r.Reason, r.Total, r.CreatedBy).Scan(&r.ID, &r.CreatedAt); err != nil {
		return err
	}
	for _, it := range r.Items {
		if err := s.db.QueryRowContext(ctx, `
			INSERT INTO sale_return_items (return_id, sale_item_id, quantity, amount, restock)
			VALUES ($1, $2, $3, $4, $5) RETURNING id;`,
			r.ID, it.SaleItemID, it.Quantity, it.Amount, it.Restock).Scan(&it.ID); err != nil {
			return err
		}
	}
	for _, p := range r.Payments {
		if err := s.db.QueryRowContext(ctx, `
			INSERT INTO sale_return_payments (return_id, method, amount, reference)
			VALUES ($1, $2, $3, NULLIF($4, '')) RETURNING id;`,
			r.ID, p.Method, p.Amount, p.Reference).Scan(&p.ID); err != nil {
			return err
		}
	}
	return nil
}

// Returned dice, de la venta saleID, cuánto se devolvió ya de cada renglón
// y cuánta plata en total. Los renglones sin devoluciones no están.
func (s *SaleReturnStore) Returned(ctx context.Context, saleID int64) (map[int64]decimal.Decimal, decimal.Decimal, error) {
	out := map[int64]decimal.Decimal{}
	rows, err := s.db.QueryContext(ctx, `
		SELECT ri.sale_item_id, SUM(ri.quantity)
		FROM sale_return_items ri JOIN sale_returns r ON r.id = ri.return_id
		WHERE r.sale_id = $1
		GROUP BY ri.sale_item_id;`, saleID)
	if err != nil {
		return nil, decimal.Zero, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var q decimal.Decimal
		if err := rows.Scan(&id, &q); err != nil {
			return nil, decimal.Zero, err
		}
		out[id] = q
	}
	if err := rows.Err(); err != nil {
		return nil, decimal.Zero, err
	}
	var money decimal.Decimal
	err = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(total), 0) FROM sale_returns WHERE sale_id = $1;`, saleID).Scan(&money)
	return out, money, err
}

// HasReturns dice si la venta tiene alguna devolución.
func (s *SaleReturnStore) HasReturns(ctx context.Context, saleID int64) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM sale_returns WHERE sale_id = $1);`, saleID).Scan(&ok)
	return ok, err
}

// Components son los componentes que dio cada renglón de combo de la venta,
// por renglón (sale_item_components, 0023).
func (s *SaleReturnStore) Components(ctx context.Context, saleID int64) (map[int64][]*model.SaleItemComponent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.sale_item_id, c.product_id, c.quantity, c.unit_cost, c.uncovered_quantity
		FROM sale_item_components c JOIN sale_items i ON i.id = c.sale_item_id
		WHERE i.sale_id = $1
		ORDER BY c.product_id;`, saleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]*model.SaleItemComponent{}
	for rows.Next() {
		c := new(model.SaleItemComponent)
		if err := rows.Scan(&c.ID, &c.SaleItemID, &c.ProductID, &c.Quantity, &c.UnitCost, &c.Uncovered); err != nil {
			return nil, err
		}
		out[c.SaleItemID] = append(out[c.SaleItemID], c)
	}
	return out, rows.Err()
}

// FindSaleByNumber es el id de la venta con ese número (la más nueva, si
// hubiera dos cajas). ErrNotFound si no hay.
func (s *SaleReturnStore) FindSaleByNumber(ctx context.Context, number int) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `SELECT id FROM sales WHERE sale_number = $1 ORDER BY id DESC LIMIT 1;`, number).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, store.ErrNotFound
	}
	return id, err
}
