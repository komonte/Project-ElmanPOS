package postgres

import (
	"context"
	"database/sql"
	"errors"

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
// sus lotes y pagos. Llena los ids y fechas en sale (y en sus renglones,
// lotes y pagos). Totales, precios y costos tienen que venir calculados.
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
		INSERT INTO sales (cashier_session_id, sale_number, status, subtotal, tax_amount, discount_amount, total)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at;`,
		sale.CashierSessionID, sale.SaleNumber, sale.Status, sale.Subtotal, sale.TaxAmount,
		sale.DiscountAmount, sale.Total).Scan(&sale.ID, &sale.CreatedAt); err != nil {
		return err
	}
	for _, it := range sale.Items {
		it.SaleID = sale.ID
		if err := s.db.QueryRowContext(ctx, `
			INSERT INTO sale_items (sale_id, product_id, quantity, unit_price, unit_cost, tax_rate)
			VALUES ($1, $2, $3, $4, $5, $6)
			RETURNING id, created_at;`,
			it.SaleID, it.ProductID, it.Quantity, it.UnitPrice, it.UnitCost, it.TaxRate,
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
// producto, en el orden en que se guardaron), sus pagos y el nombre de la
// caja: lo que lleva el ticket. No trae los lotes. ErrNotFound si no existe.
func (s *SaleStore) Get(ctx context.Context, id int64) (*model.Sale, error) {
	sale := new(model.Sale)
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.cashier_session_id, s.sale_number, s.status, s.subtotal, s.tax_amount,
		       s.discount_amount, s.total, s.created_at, r.name
		FROM sales s
		JOIN cashier_sessions cs ON cs.id = s.cashier_session_id
		JOIN cash_registers r ON r.id = cs.cash_register_id
		WHERE s.id = $1;`, id).Scan(&sale.ID, &sale.CashierSessionID, &sale.SaleNumber, &sale.Status,
		&sale.Subtotal, &sale.TaxAmount, &sale.DiscountAmount, &sale.Total, &sale.CreatedAt, &sale.RegisterName)
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
