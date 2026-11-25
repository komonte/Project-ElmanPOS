package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// PurchaseStore guarda las compras y sus renglones (02 4.21 / 4.22, F-06).
// Solo escribe filas: los lotes, movimientos, saldos y costos los arma el
// caso de uso con sus propios stores, todo en la misma transacción.
type PurchaseStore struct {
	db DBTX
}

// NewPurchaseStore recibe un DBTX: casi siempre la transacción de la
// recepción, así la compra no queda a medias.
func NewPurchaseStore(db DBTX) *PurchaseStore {
	return &PurchaseStore{db: db}
}

const purchaseColumns = `id, supplier_id, COALESCE(voucher_number, ''), status, total,
	received_at, received_by, created_at, updated_at`

func scanPurchase(row rowScanner) (*model.Purchase, error) {
	p := new(model.Purchase)
	err := row.Scan(&p.ID, &p.SupplierID, &p.VoucherNumber, &p.Status, &p.Total,
		&p.ReceivedAt, &p.ReceivedBy, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return p, nil
}

const purchaseItemColumns = `id, purchase_id, product_id, quantity, unit_cost, batch_id, created_at`

func scanPurchaseItem(row rowScanner) (*model.PurchaseItem, error) {
	it := new(model.PurchaseItem)
	err := row.Scan(&it.ID, &it.PurchaseID, &it.ProductID, &it.Quantity, &it.UnitCost,
		&it.BatchID, &it.CreatedAt)
	if err != nil {
		return nil, err
	}
	return it, nil
}

// Create guarda la cabecera de una compra recibida y la devuelve con su id y
// fechas (sin renglones: van con CreateItem). Status queda 'RECEIVED' y
// received_by, el que venga (nil hasta que haya login).
//   - ErrInvalidReference: el proveedor (o el usuario) no existe.
func (s *PurchaseStore) Create(ctx context.Context, p *model.Purchase) (*model.Purchase, error) {
	if p == nil {
		return nil, model.ErrNilPurchase
	}
	got, err := scanPurchase(s.db.QueryRowContext(ctx, `
		INSERT INTO purchases (supplier_id, voucher_number, total, received_by)
		VALUES ($1, NULLIF($2, ''), $3, $4)
		RETURNING `+purchaseColumns+`;`,
		p.SupplierID, p.VoucherNumber, p.Total, p.ReceivedBy))
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, store.ErrInvalidReference
		}
		return nil, err
	}
	return got, nil
}

// CreateItem guarda un renglón de una compra. BatchID es el lote que armó el
// caso de uso (nil si el producto no controla lotes). Lote y vencimiento
// escritos no se guardan acá: viven en product_batches.
//   - ErrInvalidReference: la compra, el producto o el lote no existen.
func (s *PurchaseStore) CreateItem(ctx context.Context, it *model.PurchaseItem) (*model.PurchaseItem, error) {
	if it == nil {
		return nil, model.ErrNilPurchaseItem
	}
	got, err := scanPurchaseItem(s.db.QueryRowContext(ctx, `
		INSERT INTO purchase_items (purchase_id, product_id, quantity, unit_cost, batch_id)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING `+purchaseItemColumns+`;`,
		it.PurchaseID, it.ProductID, it.Quantity, it.UnitCost, it.BatchID))
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, store.ErrInvalidReference
		}
		return nil, err
	}
	return got, nil
}

// GetByID devuelve una compra con sus renglones, en el orden en que se
// cargaron. ErrNotFound si no existe.
func (s *PurchaseStore) GetByID(ctx context.Context, id int64) (*model.Purchase, error) {
	p, err := scanPurchase(s.db.QueryRowContext(ctx, `
		SELECT `+purchaseColumns+` FROM purchases WHERE id = $1;`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT `+purchaseItemColumns+` FROM purchase_items
		WHERE purchase_id = $1
		ORDER BY id;`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	p.Items = make([]*model.PurchaseItem, 0)
	for rows.Next() {
		it, err := scanPurchaseItem(rows)
		if err != nil {
			return nil, err
		}
		p.Items = append(p.Items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return p, nil
}
