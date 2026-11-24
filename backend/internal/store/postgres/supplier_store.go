package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

var _ Store[model.Supplier] = (*SupplierStore)(nil)

type SupplierStore struct {
	db DBTX
}

func NewSupplierStore(db DBTX) *SupplierStore {
	return &SupplierStore{db: db}
}

func (s *SupplierStore) Create(ctx context.Context, supplier *model.Supplier) (*model.Supplier, error) {
	if supplier == nil {
		return nil, errors.New("supplier cannot be nil")
	}

	// Los opcionales vacíos ("") se guardan como NULL.
	// id, is_active y las fechas los pone la DB y vuelven por RETURNING.
	q := `INSERT INTO suppliers (name, tax_id_number, phone, email, payment_terms)
	      VALUES ($1, NULLIF($2, ''), NULLIF($3, ''), NULLIF($4, ''), NULLIF($5, ''))
	      RETURNING id, is_active, created_at, updated_at;`

	err := s.db.QueryRowContext(ctx, q, supplier.Name, supplier.TaxIDNumber, supplier.Phone, supplier.Email, supplier.PaymentTerms).Scan(&supplier.ID, &supplier.IsActive, &supplier.CreatedAt, &supplier.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, store.ErrDuplicateTaxID
		}
		return nil, err
	}
	return supplier, nil
}

func (s *SupplierStore) GetAll(ctx context.Context) ([]*model.Supplier, error) {
	// Los opcionales NULL vuelven como "" (lo inverso al NULLIF de Create). -> sino .Scan no podria insertar NULL en un string
	q := `SELECT id, name,
	             COALESCE(tax_id_number, ''), COALESCE(phone, ''),
	             COALESCE(email, ''), COALESCE(payment_terms, ''),
	             is_active, created_at, updated_at
	      FROM suppliers
	      WHERE is_active = TRUE
	      ORDER BY name;`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	suppliers := make([]*model.Supplier, 0)
	for rows.Next() {
		sp := new(model.Supplier)
		if err := rows.Scan(&sp.ID, &sp.Name, &sp.TaxIDNumber, &sp.Phone, &sp.Email, &sp.PaymentTerms, &sp.IsActive, &sp.CreatedAt, &sp.UpdatedAt); err != nil {
			return nil, err
		}

		suppliers = append(suppliers, sp)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return suppliers, nil
}

func (s *SupplierStore) GetAllIncludingInactive(ctx context.Context) ([]*model.Supplier, error) {
	q := `SELECT id, name,
	             COALESCE(tax_id_number, ''), COALESCE(phone, ''),
	             COALESCE(email, ''), COALESCE(payment_terms, ''),
	             is_active, created_at, updated_at
	      FROM suppliers
				ORDER BY is_active DESC, name;`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	suppliers := make([]*model.Supplier, 0)
	for rows.Next() {
		sp := new(model.Supplier)
		if err := rows.Scan(&sp.ID, &sp.Name, &sp.TaxIDNumber, &sp.Phone, &sp.Email, &sp.PaymentTerms, &sp.IsActive, &sp.CreatedAt, &sp.UpdatedAt); err != nil {
			return nil, err
		}

		suppliers = append(suppliers, sp)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return suppliers, nil
}

func (s *SupplierStore) GetByID(ctx context.Context, id int64) (*model.Supplier, error) {
	q := `SELECT id, name,
	             COALESCE(tax_id_number, ''), COALESCE(phone, ''),
	             COALESCE(email, ''), COALESCE(payment_terms, ''),
	             is_active, created_at, updated_at
	      FROM suppliers 
				WHERE id = $1;`

	var sp model.Supplier

	err := s.db.QueryRowContext(ctx, q, id).Scan(&sp.ID, &sp.Name, &sp.TaxIDNumber, &sp.Phone, &sp.Email, &sp.PaymentTerms, &sp.IsActive, &sp.CreatedAt, &sp.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return &sp, nil
}

func (s *SupplierStore) SearchByName(ctx context.Context, query string) ([]*model.Supplier, error) {
	q := `SELECT id, name,
	             COALESCE(tax_id_number, ''), COALESCE(phone, ''),
	             COALESCE(email, ''), COALESCE(payment_terms, ''),
	             is_active, created_at, updated_at
	      FROM suppliers 
				WHERE is_active = TRUE
				AND name ILIKE '%' || $1 || '%'
				ORDER BY name ASC
				LIMIT 20;`
	rows, err := s.db.QueryContext(ctx, q, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	suppliers := make([]*model.Supplier, 0)
	for rows.Next() {
		sp := new(model.Supplier)
		if err := rows.Scan(&sp.ID, &sp.Name, &sp.TaxIDNumber, &sp.Phone, &sp.Email, &sp.PaymentTerms, &sp.IsActive, &sp.CreatedAt, &sp.UpdatedAt); err != nil {
			return nil, err
		}
		suppliers = append(suppliers, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return suppliers, nil
}

func (s *SupplierStore) SearchAllByName(ctx context.Context, query string) ([]*model.Supplier, error) {
	q := `SELECT id, name,
	             COALESCE(tax_id_number, ''), COALESCE(phone, ''),
	             COALESCE(email, ''), COALESCE(payment_terms, ''),
	             is_active, created_at, updated_at
	      FROM suppliers 
				WHERE name ILIKE '%' || $1 || '%'
				ORDER BY is_active DESC, name ASC
				LIMIT 20;`
	rows, err := s.db.QueryContext(ctx, q, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	suppliers := make([]*model.Supplier, 0)
	for rows.Next() {
		sp := new(model.Supplier)
		if err := rows.Scan(&sp.ID, &sp.Name, &sp.TaxIDNumber, &sp.Phone, &sp.Email, &sp.PaymentTerms, &sp.IsActive, &sp.CreatedAt, &sp.UpdatedAt); err != nil {
			return nil, err
		}
		suppliers = append(suppliers, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return suppliers, nil
}

func (s *SupplierStore) Update(ctx context.Context, id int64, supplier *model.Supplier) (*model.Supplier, error) {
	if supplier == nil {
		return nil, errors.New("supplier cannot be nil")
	}

	// No toca is_active: eso pasa solo por SetActive (ver BrandStore.Update).
	q := `UPDATE suppliers
	      SET name = $1,
	          tax_id_number = NULLIF($2, ''),
	          phone = NULLIF($3, ''),
	          email = NULLIF($4, ''),
	          payment_terms = NULLIF($5, '')
	      WHERE id = $6
	      RETURNING id, is_active, created_at, updated_at;`

	err := s.db.QueryRowContext(ctx, q, supplier.Name, supplier.TaxIDNumber, supplier.Phone, supplier.Email, supplier.PaymentTerms, id).Scan(&supplier.ID, &supplier.IsActive, &supplier.CreatedAt, &supplier.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		if isUniqueViolation(err) {
			return nil, store.ErrDuplicateTaxID
		}
		return nil, err
	}

	return supplier, nil
}

func (s *SupplierStore) Delete(ctx context.Context, id int64) error {
	q := `DELETE FROM suppliers WHERE id = $1;`

	res, err := s.db.ExecContext(ctx, q, id)
	if err != nil {
		// Lo referencian products.preferred_supplier_id y las compras.
		if isForeignKeyViolation(err) {
			return store.ErrInUse
		}
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return store.ErrNotFound
	}

	return nil
}

func (s *SupplierStore) SetActive(ctx context.Context, id int64, active bool) error {
	q := `UPDATE suppliers SET is_active = $1 WHERE id = $2;`

	res, err := s.db.ExecContext(ctx, q, active, id)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return store.ErrNotFound
	}

	return nil
}

func scanSupplier(row rowScanner) (*model.Supplier, error) {
	sp := new(model.Supplier)
	err := row.Scan(&sp.ID, &sp.Name, &sp.TaxIDNumber, &sp.Phone, &sp.Email, &sp.PaymentTerms,
		&sp.IsActive, &sp.CreatedAt, &sp.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return sp, nil
}

func (s *SupplierStore) ListAll(ctx context.Context, query string, limit int) ([]*model.Supplier, int, error) {
	const where = `WHERE ($1 = '' OR name ILIKE '%' || $1 || '%')`
	return listPage(ctx, s.db,
		`SELECT count(*) FROM suppliers `+where+`;`,
		`SELECT id, name,
		        COALESCE(tax_id_number, ''), COALESCE(phone, ''),
		        COALESCE(email, ''), COALESCE(payment_terms, ''),
		        is_active, created_at, updated_at
		 FROM suppliers `+where+`
		 ORDER BY is_active DESC, name LIMIT $2;`,
		scanSupplier, query, limit)
}
