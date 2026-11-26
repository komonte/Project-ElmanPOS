package postgres

import (
	"context"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// StockAdjustmentStore guarda las salidas conocidas (02 4.26, F-09b). Solo
// agrega filas: stock_adjustments no se edita ni se borra (trigger). Los
// movimientos y el saldo los arma el caso de uso en la misma transacción.
type StockAdjustmentStore struct {
	db DBTX
}

func NewStockAdjustmentStore(db DBTX) *StockAdjustmentStore {
	return &StockAdjustmentStore{db: db}
}

// Create guarda una salida y la devuelve con su id y fecha. Notes vacío se
// guarda NULL.
//   - ErrInvalidReference: el local o el producto no existen.
//
// Un motivo que no es de salida o "otro" sin nota lo rechaza un CHECK y sale
// como error común: Validate lo revisa antes.
func (s *StockAdjustmentStore) Create(ctx context.Context, a *model.StockAdjustment) (*model.StockAdjustment, error) {
	if a == nil {
		return nil, model.ErrNilStockAdjustment
	}
	got := *a
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO stock_adjustments (store_id, product_id, reason, quantity, notes, created_by)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6)
		RETURNING id, created_at;`,
		a.StoreID, a.ProductID, a.Reason, a.Quantity, a.Notes, a.CreatedBy).Scan(&got.ID, &got.CreatedAt)
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, store.ErrInvalidReference
		}
		return nil, err
	}
	return &got, nil
}

// Recent devuelve las últimas salidas de un local (la más nueva primero),
// hasta limit, con el nombre del producto y el de quién la anotó. Vacía, no nil, si no hay.
func (s *StockAdjustmentStore) Recent(ctx context.Context, storeID int64, limit int) ([]*model.StockAdjustment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, a.store_id, a.product_id, a.reason, a.quantity, COALESCE(a.notes, ''),
		       a.created_by, a.created_at, p.name, u.name
		FROM stock_adjustments a
		JOIN products p ON p.id = a.product_id
		LEFT JOIN users u ON u.id = a.created_by
		WHERE a.store_id = $1
		ORDER BY a.created_at DESC, a.id DESC
		LIMIT $2;`, storeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]*model.StockAdjustment, 0)
	for rows.Next() {
		a := new(model.StockAdjustment)
		if err := rows.Scan(&a.ID, &a.StoreID, &a.ProductID, &a.Reason, &a.Quantity, &a.Notes,
			&a.CreatedBy, &a.CreatedAt, &a.ProductName, &a.CreatedByName); err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}
