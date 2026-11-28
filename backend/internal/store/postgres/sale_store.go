package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// SaleStore guarda y lee las ventas (02 4.16-4.19): cabecera, renglones,
// lotes de cada renglón y pagos. Solo agrega filas; anular (F-08) solo
// cambia el estado. Los movimientos de stock y el saldo los arma el caso de
// uso en la misma transacción (BR-04).
type SaleStore struct {
	db DBTX
}

func NewSaleStore(db DBTX) *SaleStore {
	return &SaleStore{db: db}
}

// NextNumber es el número de la próxima venta de una caja: sigue de sesión
// en sesión y no se repite. Quien vende lo pide con la sesión trabada
// (CashierSessionStore.LockOpen): con una sola sesión abierta por caja, dos
// ventas de la misma caja no pueden leer el mismo número.
func (s *SaleStore) NextNumber(ctx context.Context, registerID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(s.sale_number), 0) + 1
		FROM sales s
		JOIN cashier_sessions cs ON cs.id = s.cashier_session_id
		WHERE cs.cash_register_id = $1;`, registerID).Scan(&n)
	return n, err
}

// Create guarda la venta entera: cabecera (estado COMPLETED), renglones con
// sus lotes y sus componentes (combos), y pagos. Llena los ids y fechas en
// sale (y en sus renglones, lotes, componentes y pagos). Totales, precios y costos tienen que venir calculados.
// Referencia de pago vacía se guarda NULL.
//   - ErrInvalidReference: la sesión, un producto o un lote no existen.
//
// Un número repetido o un monto que no cumple un CHECK salen como error
// común: el caso de uso los evita, es un error del programa.
func (s *SaleStore) Create(ctx context.Context, sale *model.Sale) error {
	if sale == nil {
		return model.ErrNilSale
	}
	if err := s.create(ctx, sale); err != nil {
		if isForeignKeyViolation(err) {
			return store.ErrInvalidReference
		}
		return err
	}
	return nil
}

func (s *SaleStore) create(ctx context.Context, sale *model.Sale) error {
	sale.Status = model.SaleCompleted
	if err := s.db.QueryRowContext(ctx, `
		INSERT INTO sales (cashier_session_id, sale_number, status, subtotal, tax_amount, discount_amount, total,
		                   created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id, created_at;`,
		sale.CashierSessionID, sale.SaleNumber, sale.Status, sale.Subtotal, sale.TaxAmount,
		sale.DiscountAmount, sale.Total, sale.CreatedBy).Scan(&sale.ID, &sale.CreatedAt); err != nil {
		return err
	}
	for _, it := range sale.Items {
		it.SaleID = sale.ID
		if err := s.db.QueryRowContext(ctx, `
			INSERT INTO sale_items (sale_id, product_id, quantity, unit_price, unit_cost, tax_rate,
			                        uncovered_quantity, scanned_code)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8, ''))
			RETURNING id, created_at;`,
			it.SaleID, it.ProductID, it.Quantity, it.UnitPrice, it.UnitCost, it.TaxRate, it.Uncovered,
			it.ScannedCode,
		).Scan(&it.ID, &it.CreatedAt); err != nil {
			return err
		}
		for _, b := range it.Batches {
			b.SaleItemID = it.ID
			if err := s.db.QueryRowContext(ctx, `
				INSERT INTO sale_item_batches (sale_item_id, batch_id, quantity, unit_cost)
				VALUES ($1, $2, $3, $4) RETURNING id;`,
				b.SaleItemID, b.BatchID, b.Quantity, b.UnitCost).Scan(&b.ID); err != nil {
				return err
			}
		}
		for _, c := range it.Components {
			c.SaleItemID = it.ID
			if err := s.db.QueryRowContext(ctx, `
				INSERT INTO sale_item_components (sale_item_id, product_id, quantity, unit_cost, uncovered_quantity)
				VALUES ($1, $2, $3, $4, $5) RETURNING id;`,
				c.SaleItemID, c.ProductID, c.Quantity, c.UnitCost, c.Uncovered).Scan(&c.ID); err != nil {
				return err
			}
		}
	}
	for _, p := range sale.Payments {
		p.SaleID = sale.ID
		if err := s.db.QueryRowContext(ctx, `
			INSERT INTO payments (sale_id, method, amount, reference)
			VALUES ($1, $2, $3, NULLIF($4, ''))
			RETURNING id, created_at;`,
			p.SaleID, p.Method, p.Amount, p.Reference).Scan(&p.ID, &p.CreatedAt); err != nil {
			return err
		}
	}
	return nil
}

// Get devuelve una venta con sus renglones (con nombre y tipo de venta del
// producto, en el orden en que se guardaron), sus pagos, el nombre de la
// caja y el nombre y la dirección del local: lo que lleva el ticket. Si está
// anulada, también cuándo y por qué. No trae los lotes. ErrNotFound si no existe.
func (s *SaleStore) Get(ctx context.Context, id int64) (*model.Sale, error) {
	sale := new(model.Sale)
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.cashier_session_id, s.sale_number, s.status, s.subtotal, s.tax_amount,
		       s.discount_amount, s.total, s.created_at, s.voided_at, COALESCE(s.void_reason, ''),
		       r.name, st.name, COALESCE(st.address, ''), s.created_by, u.name
		FROM sales s
		JOIN cashier_sessions cs ON cs.id = s.cashier_session_id
		JOIN cash_registers r ON r.id = cs.cash_register_id
		JOIN stores st ON st.id = r.store_id
		LEFT JOIN users u ON u.id = s.created_by
		WHERE s.id = $1;`, id).Scan(&sale.ID, &sale.CashierSessionID, &sale.SaleNumber, &sale.Status,
		&sale.Subtotal, &sale.TaxAmount, &sale.DiscountAmount, &sale.Total, &sale.CreatedAt, &sale.VoidedAt,
		&sale.VoidReason, &sale.RegisterName, &sale.StoreName, &sale.StoreAddress, &sale.CreatedBy,
		&sale.CreatedByName)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `
		SELECT i.id, i.sale_id, i.product_id, i.quantity, i.unit_price, i.unit_cost, i.tax_rate,
		       i.created_at, p.name, p.sale_type
		FROM sale_items i
		JOIN products p ON p.id = i.product_id
		WHERE i.sale_id = $1
		ORDER BY i.id;`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		it := new(model.SaleItem)
		if err := rows.Scan(&it.ID, &it.SaleID, &it.ProductID, &it.Quantity, &it.UnitPrice, &it.UnitCost,
			&it.TaxRate, &it.CreatedAt, &it.ProductName, &it.SaleType); err != nil {
			return nil, err
		}
		sale.Items = append(sale.Items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	prows, err := s.db.QueryContext(ctx, `
		SELECT id, sale_id, method, amount, COALESCE(reference, ''), created_at
		FROM payments WHERE sale_id = $1 ORDER BY id;`, id)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	for prows.Next() {
		p := new(model.Payment)
		if err := prows.Scan(&p.ID, &p.SaleID, &p.Method, &p.Amount, &p.Reference, &p.CreatedAt); err != nil {
			return nil, err
		}
		sale.Payments = append(sale.Payments, p)
	}
	return sale, prows.Err()
}

// PaidByMethod suma los pagos de las ventas COMPLETED de una sesión, por
// medio: lo esperado del arqueo (F-12). Las anuladas no cuentan (su plata
// se devolvió). Un medio sin pagos no está en el map.
func (s *SaleStore) PaidByMethod(ctx context.Context, sessionID int64) (map[string]decimal.Decimal, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.method, SUM(p.amount)
		FROM payments p
		JOIN sales s ON s.id = p.sale_id
		WHERE s.cashier_session_id = $1 AND s.status = 'COMPLETED'
		GROUP BY p.method;`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paid := make(map[string]decimal.Decimal)
	for rows.Next() {
		var method string
		var sum decimal.Decimal
		if err := rows.Scan(&method, &sum); err != nil {
			return nil, err
		}
		paid[method] = sum
	}
	return paid, rows.Err()
}

// ListBySession devuelve las ventas de una sesión, la última primero, con
// sus pagos (sin renglones): la lista de la caja (F-08) y el resumen del
// cierre (F-12). Vacía, no nil, si no tiene.
func (s *SaleStore) ListBySession(ctx context.Context, sessionID int64) ([]*model.Sale, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.cashier_session_id, s.sale_number, s.status, s.subtotal, s.tax_amount, s.discount_amount,
		       s.total, s.created_at, s.voided_at, COALESCE(s.void_reason, ''), s.created_by, u.name
		FROM sales s
		LEFT JOIN users u ON u.id = s.created_by
		WHERE s.cashier_session_id = $1
		ORDER BY s.sale_number DESC;`, sessionID)
	if err != nil {
		return nil, err
	}
	list := make([]*model.Sale, 0)
	byID := make(map[int64]*model.Sale)
	for rows.Next() {
		sale := new(model.Sale)
		if err := rows.Scan(&sale.ID, &sale.CashierSessionID, &sale.SaleNumber, &sale.Status, &sale.Subtotal,
			&sale.TaxAmount, &sale.DiscountAmount, &sale.Total, &sale.CreatedAt, &sale.VoidedAt,
			&sale.VoidReason, &sale.CreatedBy, &sale.CreatedByName); err != nil {
			rows.Close()
			return nil, err
		}
		list = append(list, sale)
		byID[sale.ID] = sale
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	prows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.sale_id, p.method, p.amount, COALESCE(p.reference, ''), p.created_at
		FROM payments p
		JOIN sales s ON s.id = p.sale_id
		WHERE s.cashier_session_id = $1
		ORDER BY p.id;`, sessionID)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	for prows.Next() {
		p := new(model.Payment)
		if err := prows.Scan(&p.ID, &p.SaleID, &p.Method, &p.Amount, &p.Reference, &p.CreatedAt); err != nil {
			return nil, err
		}
		if sale := byID[p.SaleID]; sale != nil {
			sale.Payments = append(sale.Payments, p)
		}
	}
	return list, prows.Err()
}

// Lock traba la cabecera de una venta hasta el final de la transacción y la
// devuelve (sin renglones ni pagos): anular (F-08) la lee trabada, así dos
// anulaciones a la vez no devuelven el stock dos veces. ErrNotFound si no
// existe.
func (s *SaleStore) Lock(ctx context.Context, id int64) (*model.Sale, error) {
	sale := new(model.Sale)
	err := s.db.QueryRowContext(ctx, `
		SELECT id, cashier_session_id, sale_number, status, subtotal, tax_amount, discount_amount, total,
		       created_at, voided_at, COALESCE(void_reason, '')
		FROM sales WHERE id = $1
		FOR UPDATE;`, id).Scan(&sale.ID, &sale.CashierSessionID, &sale.SaleNumber, &sale.Status,
		&sale.Subtotal, &sale.TaxAmount, &sale.DiscountAmount, &sale.Total, &sale.CreatedAt, &sale.VoidedAt,
		&sale.VoidReason)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return sale, nil
}

// Void anula una venta COMPLETED (F-08): estado VOIDED, la hora y el motivo
// (ya validado: chk_sales_void de la 0018 rechaza uno en blanco). Devuelve
// la hora de la anulación. El stock lo devuelve el caso de uso en la misma
// transacción.
//   - ErrSaleNotCompleted: no existe o ya estaba anulada.
func (s *SaleStore) Void(ctx context.Context, id int64, reason string) (string, error) {
	var voidedAt string
	err := s.db.QueryRowContext(ctx, `
		UPDATE sales SET status = 'VOIDED', voided_at = now(), void_reason = $2
		WHERE id = $1 AND status = 'COMPLETED'
		RETURNING voided_at;`, id, reason).Scan(&voidedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", store.ErrSaleNotCompleted
	}
	return voidedAt, err
}
