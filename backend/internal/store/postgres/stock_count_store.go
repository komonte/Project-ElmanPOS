package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// StockCountStore guarda los conteos de stock (02 4.14, F-09): el conteo, un
// renglón por producto contado y las cargas por sector. Solo escribe filas:
// los movimientos y saldos que deja la confirmación los arma el caso de uso
// con sus propios stores, en la misma transacción.
//
// El total contado de un renglón (counted_quantity) es la suma de sus
// cargas: AddEntry y DeleteEntry lo mantienen. Conviene llamarlos dentro de
// una transacción, para que carga y total cambien juntos.
type StockCountStore struct {
	db DBTX
}

func NewStockCountStore(db DBTX) *StockCountStore {
	return &StockCountStore{db: db}
}

const stockCountColumns = `id, store_id, status, created_by, created_at, updated_at, confirmed_at`

func scanStockCount(row rowScanner) (*model.StockCount, error) {
	c := new(model.StockCount)
	err := row.Scan(&c.ID, &c.StoreID, &c.Status, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt, &c.ConfirmedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// expiry_date es DATE: se lee como texto, igual que en los lotes.
const stockCountItemColumns = `id, count_id, product_id, system_quantity, counted_quantity, unit_cost,
	COALESCE(to_char(expiry_date, 'YYYY-MM-DD'), ''), created_at, updated_at`

func scanStockCountItem(row rowScanner) (*model.StockCountItem, error) {
	it := new(model.StockCountItem)
	err := row.Scan(&it.ID, &it.CountID, &it.ProductID, &it.SystemQuantity, &it.CountedQuantity,
		&it.UnitCost, &it.ExpiryDate, &it.CreatedAt, &it.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return it, nil
}

// CreateDraft abre un conteo en borrador en un local.
//   - ErrCountDraftExists: el local ya tiene uno en borrador (índice único
//     parcial de 0015).
//   - ErrInvalidReference: el local no existe.
func (s *StockCountStore) CreateDraft(ctx context.Context, storeID int64) (*model.StockCount, error) {
	c, err := scanStockCount(s.db.QueryRowContext(ctx, `
		INSERT INTO stock_counts (store_id) VALUES ($1)
		RETURNING `+stockCountColumns+`;`, storeID))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, store.ErrCountDraftExists
		}
		if isForeignKeyViolation(err) {
			return nil, store.ErrInvalidReference
		}
		return nil, err
	}
	return c, nil
}

// Draft devuelve el conteo en borrador del local, sin renglones.
// ErrNotFound si no hay ninguno.
func (s *StockCountStore) Draft(ctx context.Context, storeID int64) (*model.StockCount, error) {
	c, err := scanStockCount(s.db.QueryRowContext(ctx, `
		SELECT `+stockCountColumns+` FROM stock_counts
		WHERE store_id = $1 AND status = 'DRAFT';`, storeID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return c, nil
}

// LockDraft bloquea un conteo en borrador hasta que termine la transacción y
// lo devuelve sin renglones. Todo lo que cambia un conteo (cargar, quitar,
// confirmar, descartar) lo llama primero: así una carga no se cuela mientras
// otro confirma.
//   - ErrNotFound: no existe.
//   - ErrCountNotDraft: ya está confirmado.
func (s *StockCountStore) LockDraft(ctx context.Context, id int64) (*model.StockCount, error) {
	c, err := scanStockCount(s.db.QueryRowContext(ctx, `
		SELECT `+stockCountColumns+` FROM stock_counts WHERE id = $1 FOR UPDATE;`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	if c.Status != model.CountDraft {
		return nil, store.ErrCountNotDraft
	}
	return c, nil
}

// GetByID devuelve un conteo con sus renglones (por nombre de producto) y
// las cargas de cada uno (en el orden en que se hicieron). Cada renglón trae
// además del producto su nombre, si controla lotes y su costo promedio
// vigente (para revisar antes de confirmar). ErrNotFound si no existe.
func (s *StockCountStore) GetByID(ctx context.Context, id int64) (*model.StockCount, error) {
	c, err := scanStockCount(s.db.QueryRowContext(ctx, `
		SELECT `+stockCountColumns+` FROM stock_counts WHERE id = $1;`, id))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.count_id, i.product_id, i.system_quantity, i.counted_quantity, i.unit_cost,
		       COALESCE(to_char(i.expiry_date, 'YYYY-MM-DD'), ''), i.created_at, i.updated_at,
		       p.name, p.controls_batches, c.avg_cost
		FROM stock_count_items i
		JOIN products p ON p.id = i.product_id
		LEFT JOIN LATERAL (
			SELECT avg_cost FROM product_costs WHERE product_id = i.product_id ORDER BY valid_from DESC LIMIT 1
		) c ON TRUE
		WHERE i.count_id = $1
		ORDER BY p.name, i.id;`, id)
	if err != nil {
		return nil, err
	}
	c.Items = make([]*model.StockCountItem, 0)
	byID := map[int64]*model.StockCountItem{}
	for rows.Next() {
		it := new(model.StockCountItem)
		err := rows.Scan(&it.ID, &it.CountID, &it.ProductID, &it.SystemQuantity, &it.CountedQuantity,
			&it.UnitCost, &it.ExpiryDate, &it.CreatedAt, &it.UpdatedAt,
			&it.ProductName, &it.ControlsBatches, &it.AvgCost)
		if err != nil {
			rows.Close()
			return nil, err
		}
		it.Entries = make([]*model.StockCountEntry, 0)
		c.Items = append(c.Items, it)
		byID[it.ID] = it
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.QueryContext(ctx, `
		SELECT e.id, e.item_id, i.product_id, e.sector, e.quantity, e.created_at
		FROM stock_count_entries e
		JOIN stock_count_items i ON i.id = e.item_id
		WHERE i.count_id = $1
		ORDER BY e.id;`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		e := new(model.StockCountEntry)
		if err := rows.Scan(&e.ID, &e.ItemID, &e.ProductID, &e.Sector, &e.Quantity, &e.CreatedAt); err != nil {
			return nil, err
		}
		if it := byID[e.ItemID]; it != nil {
			it.Entries = append(it.Entries, e)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return c, nil
}

// Item devuelve el renglón de un producto en un conteo, sin cargas.
// ErrNotFound si el producto todavía no se contó ahí.
func (s *StockCountStore) Item(ctx context.Context, countID, productID int64) (*model.StockCountItem, error) {
	it, err := scanStockCountItem(s.db.QueryRowContext(ctx, `
		SELECT `+stockCountItemColumns+` FROM stock_count_items
		WHERE count_id = $1 AND product_id = $2;`, countID, productID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return it, nil
}

// CreateItem crea el renglón de un producto con la foto del saldo
// (systemQty) y nada contado todavía: las cantidades llegan con AddEntry.
//   - ErrDuplicate: el producto ya tiene renglón en el conteo.
//   - ErrInvalidReference: el conteo o el producto no existen.
func (s *StockCountStore) CreateItem(ctx context.Context, countID, productID int64, systemQty decimal.Decimal) (*model.StockCountItem, error) {
	it, err := scanStockCountItem(s.db.QueryRowContext(ctx, `
		INSERT INTO stock_count_items (count_id, product_id, system_quantity, counted_quantity)
		VALUES ($1, $2, $3, 0)
		RETURNING `+stockCountItemColumns+`;`, countID, productID, systemQty))
	if err != nil {
		if isUniqueViolation(err) {
			return nil, store.ErrDuplicate
		}
		if isForeignKeyViolation(err) {
			return nil, store.ErrInvalidReference
		}
		return nil, err
	}
	return it, nil
}

// AddEntry guarda una carga en un renglón y suma su cantidad al total
// contado. Devuelve la carga (sin ProductID: lo sabe quien llama).
//   - ErrInvalidReference: el renglón no existe.
func (s *StockCountStore) AddEntry(ctx context.Context, itemID int64, sector string, qty decimal.Decimal) (*model.StockCountEntry, error) {
	e := new(model.StockCountEntry)
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO stock_count_entries (item_id, sector, quantity) VALUES ($1, $2, $3)
		RETURNING id, item_id, sector, quantity, created_at;`, itemID, sector, qty).
		Scan(&e.ID, &e.ItemID, &e.Sector, &e.Quantity, &e.CreatedAt)
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, store.ErrInvalidReference
		}
		return nil, err
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE stock_count_items SET counted_quantity = counted_quantity + $2 WHERE id = $1;`,
		itemID, qty); err != nil {
		return nil, err
	}
	return e, nil
}

// DeleteEntry borra una carga de un conteo y resta su cantidad del total.
// Si era la última carga del renglón, borra también el renglón: el producto
// vuelve a "no contado" y la confirmación no lo toca. Devuelve si borró el
// renglón.
//   - ErrNotFound: la carga no existe o es de otro conteo.
func (s *StockCountStore) DeleteEntry(ctx context.Context, countID, entryID int64) (itemDeleted bool, err error) {
	var itemID int64
	var qty decimal.Decimal
	err = s.db.QueryRowContext(ctx, `
		DELETE FROM stock_count_entries e
		USING stock_count_items i
		WHERE e.id = $2 AND i.id = e.item_id AND i.count_id = $1
		RETURNING e.item_id, e.quantity;`, countID, entryID).Scan(&itemID, &qty)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, store.ErrNotFound
		}
		return false, err
	}

	res, err := s.db.ExecContext(ctx, `
		DELETE FROM stock_count_items i
		WHERE i.id = $1
		  AND NOT EXISTS (SELECT 1 FROM stock_count_entries e WHERE e.item_id = i.id);`, itemID)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 1 {
		return n == 1, err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE stock_count_items SET counted_quantity = counted_quantity - $2 WHERE id = $1;`, itemID, qty)
	return false, err
}

// SetItemDetails guarda el costo (nil = sin costo) y el vencimiento (vacío =
// sin vencimiento) de un renglón del conteo. ErrNotFound si el renglón no
// existe o es de otro conteo.
func (s *StockCountStore) SetItemDetails(ctx context.Context, countID, itemID int64, unitCost *decimal.Decimal, expiry string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE stock_count_items
		SET unit_cost = $3, expiry_date = NULLIF($4, '')::date
		WHERE id = $2 AND count_id = $1;`, countID, itemID, unitCost, expiry)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// Sectors devuelve los sectores ya usados en cualquier conteo, en orden
// alfabético, para sugerirlos al cargar ("Góndola 3", "Depósito").
func (s *StockCountStore) Sectors(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT sector FROM stock_count_entries ORDER BY sector;`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sectors := make([]string, 0)
	for rows.Next() {
		var sector string
		if err := rows.Scan(&sector); err != nil {
			return nil, err
		}
		sectors = append(sectors, sector)
	}
	return sectors, rows.Err()
}

// Confirm pasa un conteo de DRAFT a CONFIRMED con la fecha de ahora y lo
// devuelve sin renglones. No mueve stock: eso lo hace el caso de uso en la
// misma transacción.
//   - ErrNotFound: no existe.
//   - ErrCountNotDraft: ya estaba confirmado.
func (s *StockCountStore) Confirm(ctx context.Context, id int64) (*model.StockCount, error) {
	c, err := scanStockCount(s.db.QueryRowContext(ctx, `
		UPDATE stock_counts SET status = 'CONFIRMED', confirmed_at = now()
		WHERE id = $1 AND status = 'DRAFT'
		RETURNING `+stockCountColumns+`;`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, s.notDraftOrMissing(ctx, id)
	}
	return c, err
}

// DeleteDraft descarta un conteo en borrador con sus renglones y cargas
// (CASCADE). No dejó movimientos, así que no queda rastro en el stock.
//   - ErrNotFound: no existe.
//   - ErrCountNotDraft: está confirmado (no se borra nunca).
func (s *StockCountStore) DeleteDraft(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `
		DELETE FROM stock_counts WHERE id = $1 AND status = 'DRAFT';`, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return s.notDraftOrMissing(ctx, id)
	}
	return nil
}

// notDraftOrMissing dice por qué un conteo no se pudo tocar como borrador.
func (s *StockCountStore) notDraftOrMissing(ctx context.Context, id int64) error {
	var exists bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM stock_counts WHERE id = $1);`, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return store.ErrCountNotDraft
	}
	return store.ErrNotFound
}
