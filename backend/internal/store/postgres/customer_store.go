package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/lib/pq"
	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// ErrCustomerNameTaken: ya hay un cliente con ese nombre (uq_customers_name_lower).
var ErrCustomerNameTaken = errors.New("there is already a customer with that name")

// CustomerStore guarda los clientes (cuenta corriente, migración 0025).
// Los datos llegan validados (model.Customer.Validate).
type CustomerStore struct {
	db DBTX
}

func NewCustomerStore(db DBTX) *CustomerStore {
	return &CustomerStore{db: db}
}

const customerSelect = `
	SELECT id, name, COALESCE(phone, ''), COALESCE(notes, ''), is_active, created_by, created_at, updated_at
	FROM customers`

func scanCustomer(row rowScanner) (*model.Customer, error) {
	c := new(model.Customer)
	err := row.Scan(&c.ID, &c.Name, &c.Phone, &c.Notes, &c.IsActive, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *CustomerStore) query(ctx context.Context, q string, args ...any) ([]*model.Customer, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]*model.Customer, 0)
	for rows.Next() {
		c, err := scanCustomer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// List devuelve los clientes que coinciden con query en el nombre o el
// teléfono (vacía: todos), activos primero y por nombre, hasta limit.
func (s *CustomerStore) List(ctx context.Context, query string, limit int) ([]*model.Customer, error) {
	return s.query(ctx, customerSelect+`
		WHERE $1 = '' OR name ILIKE '%' || $1 || '%' OR phone ILIKE '%' || $1 || '%'
		ORDER BY is_active DESC, lower(name)
		LIMIT $2;`, query, limit)
}

// Search busca entre los activos por nombre o teléfono, hasta 20: el
// buscador de la caja.
func (s *CustomerStore) Search(ctx context.Context, query string) ([]*model.Customer, error) {
	return s.query(ctx, customerSelect+`
		WHERE is_active AND (name ILIKE '%' || $1 || '%' OR phone ILIKE '%' || $1 || '%')
		ORDER BY lower(name)
		LIMIT 20;`, query)
}

// GetByID devuelve un cliente. store.ErrNotFound si no existe.
func (s *CustomerStore) GetByID(ctx context.Context, id int64) (*model.Customer, error) {
	c, err := scanCustomer(s.db.QueryRowContext(ctx, customerSelect+` WHERE id = $1;`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, store.ErrNotFound
	}
	return c, err
}

// Create guarda un cliente nuevo (activo) y llena id y fechas.
// ErrCustomerNameTaken si el nombre ya está.
func (s *CustomerStore) Create(ctx context.Context, c *model.Customer) error {
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO customers (name, phone, notes, created_by)
		VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), $4)
		RETURNING id, is_active, created_at, updated_at;`, c.Name, c.Phone, c.Notes, c.CreatedBy,
	).Scan(&c.ID, &c.IsActive, &c.CreatedAt, &c.UpdatedAt)
	if isUniqueViolation(err) {
		return ErrCustomerNameTaken
	}
	return err
}

// Update cambia nombre, teléfono y nota. store.ErrNotFound si no existe;
// ErrCustomerNameTaken si el nombre es de otro.
func (s *CustomerStore) Update(ctx context.Context, c *model.Customer) error {
	err := s.db.QueryRowContext(ctx, `
		UPDATE customers SET name = $2, phone = NULLIF($3, ''), notes = NULLIF($4, '')
		WHERE id = $1
		RETURNING is_active, created_by, created_at, updated_at;`, c.ID, c.Name, c.Phone, c.Notes,
	).Scan(&c.IsActive, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return store.ErrNotFound
	case isUniqueViolation(err):
		return ErrCustomerNameTaken
	}
	return err
}

// SetActive activa o desactiva un cliente. store.ErrNotFound si no existe.
func (s *CustomerStore) SetActive(ctx context.Context, id int64, active bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE customers SET is_active = $2 WHERE id = $1;`, id, active)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// debtSelect es la deuda de cada cliente: lo que se le fió (pagos ACCOUNT
// de ventas COMPLETED). Una venta anulada deja de contar sola.
const debtSelect = `
	SELECT s.customer_id, SUM(p.amount) AS debt
	FROM payments p JOIN sales s ON s.id = p.sale_id
	WHERE p.method = 'ACCOUNT' AND s.status = 'COMPLETED' AND s.customer_id IS NOT NULL
	GROUP BY s.customer_id`

// Debts devuelve cuánto debe cada uno de los clientes ids. Los que no deben
// nada no están en el mapa.
func (s *CustomerStore) Debts(ctx context.Context, ids []int64) (map[int64]decimal.Decimal, error) {
	out := map[int64]decimal.Decimal{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT customer_id, debt FROM (`+debtSelect+`) d
		WHERE customer_id = ANY($1) AND debt <> 0;`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var debt decimal.Decimal
		if err := rows.Scan(&id, &debt); err != nil {
			return nil, err
		}
		out[id] = debt
	}
	return out, rows.Err()
}
