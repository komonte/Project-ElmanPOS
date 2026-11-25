package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// BatchStore guarda los lotes (02 4.11). La recepción de compras (F-06) los
// crea; las ventas (F-07) los van a descontar.
type BatchStore struct {
	db DBTX
}

// NewBatchStore recibe un DBTX: la transacción de la recepción.
func NewBatchStore(db DBTX) *BatchStore {
	return &BatchStore{db: db}
}

// expiry_date es DATE: se lee como texto "2027-03-31" para no pasar por
// time.Time (un DATE no tiene hora ni zona). NULL se lee vacío.
const batchColumns = `id, product_id, batch_number, COALESCE(to_char(expiry_date, 'YYYY-MM-DD'), ''),
	original_quantity, remaining_quantity, unit_cost, is_consumed, created_at, updated_at`

func scanBatch(row rowScanner) (*model.ProductBatch, error) {
	b := new(model.ProductBatch)
	err := row.Scan(&b.ID, &b.ProductID, &b.BatchNumber, &b.ExpiryDate,
		&b.OriginalQuantity, &b.RemainingQuantity, &b.UnitCost, &b.IsConsumed,
		&b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// Create guarda un lote recién recibido: lo que queda es todo lo que entró
// (remaining = original = OriginalQuantity). ExpiryDate vacío se guarda NULL.
//   - ErrInvalidReference: el producto no existe.
func (s *BatchStore) Create(ctx context.Context, b *model.ProductBatch) (*model.ProductBatch, error) {
	if b == nil {
		return nil, model.ErrNilBatch
	}
	got, err := scanBatch(s.db.QueryRowContext(ctx, `
		INSERT INTO product_batches
			(product_id, batch_number, expiry_date, original_quantity, remaining_quantity, unit_cost)
		VALUES ($1, $2, NULLIF($3, '')::date, $4, $4, $5)
		RETURNING `+batchColumns+`;`,
		b.ProductID, b.BatchNumber, b.ExpiryDate, b.OriginalQuantity, b.UnitCost))
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, store.ErrInvalidReference
		}
		return nil, err
	}
	return got, nil
}

// AddQuantity suma cantidad a un lote de la misma recepción: dos renglones
// con el mismo número de lote, vencimiento y costo se juntan en un solo lote
// (F-06). Suma a lo que entró y a lo que queda. ErrNotFound si no existe.
//
// Si el costo es distinto no se llama: quedan dos lotes con el mismo número,
// cada uno con su costo exacto (el CMV de las ventas sale de ahí).
func (s *BatchStore) AddQuantity(ctx context.Context, id int64, qty decimal.Decimal) (*model.ProductBatch, error) {
	got, err := scanBatch(s.db.QueryRowContext(ctx, `
		UPDATE product_batches
		SET original_quantity = original_quantity + $2,
		    remaining_quantity = remaining_quantity + $2
		WHERE id = $1
		RETURNING `+batchColumns+`;`, id, qty))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return got, nil
}
