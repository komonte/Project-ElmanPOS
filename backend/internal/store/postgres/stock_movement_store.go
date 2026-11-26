package postgres

import (
	"context"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// StockMovementStore escribe el libro de stock (02 4.12). Solo agrega filas:
// stock_movements no se edita ni se borra (trigger, BR-05).
type StockMovementStore struct {
	db DBTX
}

// NewStockMovementStore recibe un DBTX: la transacción que también actualiza
// el saldo, así movimiento y saldo cambian juntos (BR-04).
func NewStockMovementStore(db DBTX) *StockMovementStore {
	return &StockMovementStore{db: db}
}

// Create guarda un movimiento y lo devuelve con su id y fecha. Lo usan la
// recepción de compras (purchase_id, cantidad positiva) y la confirmación de
// un conteo (count_id, ADJUSTMENT con reason COUNT, cantidad con signo).
// Reason vacío se guarda NULL.
//   - ErrInvalidReference: el producto, el local, el lote, la compra o el
//     conteo no existen.
//
// Un movimiento mal armado (signo equivocado para el tipo, sin origen) lo
// rechaza un CHECK de la DB y sale como error común: es un error del
// programa, no del usuario.
func (s *StockMovementStore) Create(ctx context.Context, m *model.StockMovement) (*model.StockMovement, error) {
	if m == nil {
		return nil, model.ErrNilStockMovement
	}
	got := new(model.StockMovement)
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO stock_movements
			(product_id, store_id, batch_id, movement_type, reason, quantity, unit_cost,
			 purchase_id, count_id, performed_by)
		VALUES ($1, $2, $3, $4, NULLIF($5, '')::adjustment_reason, $6, $7, $8, $9, $10)
		RETURNING id, product_id, store_id, batch_id, movement_type, COALESCE(reason::text, ''),
			quantity, unit_cost, purchase_id, count_id, performed_by, created_at;`,
		m.ProductID, m.StoreID, m.BatchID, m.MovementType, m.Reason, m.Quantity, m.UnitCost,
		m.PurchaseID, m.CountID, m.PerformedBy,
	).Scan(&got.ID, &got.ProductID, &got.StoreID, &got.BatchID, &got.MovementType, &got.Reason,
		&got.Quantity, &got.UnitCost, &got.PurchaseID, &got.CountID, &got.PerformedBy, &got.CreatedAt)
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, store.ErrInvalidReference
		}
		return nil, err
	}
	return got, nil
}
