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
// crea; los conteos (F-09) y las ventas (F-07) los descuentan (TakeFIFO).
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

// TakeFIFO descuenta qty de los lotes con mercadería de un producto, primero
// el que vence antes (FIFO por vencimiento, P-04). Los lotes sin vencimiento
// van al final y, entre iguales, el más viejo primero. Un lote que queda en
// 0 se marca consumido.
//
// Devuelve cuánto salió de cada lote y lo que no alcanzó a salir (left): si
// los lotes suman menos que qty (stock anterior a que el producto controlara
// lotes, o un inventario inicial sin lote), el resto queda sin lote y quien
// llama decide qué hacer. qty tiene que ser positiva.
//
// Bloquea los lotes que lee (FOR UPDATE) hasta que termine la transacción:
// otra salida del mismo producto espera y lee lo que quedó.
func (s *BatchStore) TakeFIFO(ctx context.Context, productID int64, qty decimal.Decimal) (takes []model.BatchTake, left decimal.Decimal, err error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, remaining_quantity, unit_cost
		FROM product_batches
		WHERE product_id = $1 AND remaining_quantity > 0
		ORDER BY expiry_date NULLS LAST, created_at, id
		FOR UPDATE;`, productID)
	if err != nil {
		return nil, decimal.Zero, err
	}
	type open struct {
		id        int64
		remaining decimal.Decimal
		unitCost  decimal.Decimal
	}
	var batches []open
	for rows.Next() {
		var b open
		if err := rows.Scan(&b.id, &b.remaining, &b.unitCost); err != nil {
			rows.Close()
			return nil, decimal.Zero, err
		}
		batches = append(batches, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, decimal.Zero, err
	}

	// Se leen todos antes de actualizar: no se puede usar la conexión para
	// otra consulta mientras las filas siguen abiertas.
	left = qty
	for _, b := range batches {
		if !left.IsPositive() {
			break
		}
		take := decimal.Min(left, b.remaining)
		if _, err := s.db.ExecContext(ctx, `
			UPDATE product_batches
			SET remaining_quantity = remaining_quantity - $2,
			    is_consumed = (remaining_quantity - $2 = 0)
			WHERE id = $1;`, b.id, take); err != nil {
			return nil, decimal.Zero, err
		}
		takes = append(takes, model.BatchTake{BatchID: b.id, Quantity: take, UnitCost: b.unitCost})
		left = left.Sub(take)
	}
	return takes, left, nil
}
