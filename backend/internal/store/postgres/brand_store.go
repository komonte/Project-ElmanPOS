package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// Falla al compilar si BrandStore deja de cumplir la interfaz Store.
var _ Store[model.Brand] = (*BrandStore)(nil)

type BrandStore struct {
	db DBTX
}

func NewBrandStore(db DBTX) *BrandStore {
	return &BrandStore{db: db}
}

func (s *BrandStore) Create(ctx context.Context, brand *model.Brand) (*model.Brand, error) {
	if brand == nil {
		return nil, errors.New("brand cannot be nil")
	}

	q := `INSERT INTO brands (name) VALUES ($1) RETURNING id, is_active, created_at, updated_at;`
	err := s.db.QueryRowContext(ctx, q, brand.Name).Scan(&brand.ID, &brand.IsActive, &brand.CreatedAt, &brand.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, store.ErrDuplicateName
		}
		return nil, err
	}
	return brand, nil
}

func (s *BrandStore) GetAll(ctx context.Context) ([]*model.Brand, error) {
	q := `SELECT id, name, is_active, created_at, updated_at FROM brands WHERE is_active = TRUE;`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	brands := make([]*model.Brand, 0)
	for rows.Next() {
		b := new(model.Brand)
		if err := rows.Scan(&b.ID, &b.Name, &b.IsActive, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}

		brands = append(brands, b)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return brands, nil
}

func (s *BrandStore) GetAllIncludingInactive(ctx context.Context) ([]*model.Brand, error) {
	q := `SELECT id, name, is_active, created_at, updated_at FROM brands ORDER BY is_active DESC, name ASC;`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	brands := make([]*model.Brand, 0)
	for rows.Next() {
		b := new(model.Brand)
		if err := rows.Scan(&b.ID, &b.Name, &b.IsActive, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}

		brands = append(brands, b)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return brands, nil
}

func (s *BrandStore) GetByID(ctx context.Context, id int64) (*model.Brand, error) {
	q := `SELECT id, name, is_active, created_at, updated_at FROM brands WHERE id = $1;`

	var b model.Brand

	err := s.db.QueryRowContext(ctx, q, id).Scan(&b.ID, &b.Name, &b.IsActive, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}

	return &b, nil
}

// GetByName busca una marca por su nombre exacto, activa o no. El nombre
// tiene que venir normalizado (model.Brand.Validate: mayúsculas, sin espacios
// de más), que es como se guarda. ErrNotFound si no existe.
//
// La usa el alta de producto con marca nueva: antes de crearla, para usar la
// que ya existe o avisar que está desactivada.
func (s *BrandStore) GetByName(ctx context.Context, name string) (*model.Brand, error) {
	q := `SELECT id, name, is_active, created_at, updated_at FROM brands WHERE name = $1;`

	var b model.Brand
	err := s.db.QueryRowContext(ctx, q, name).Scan(&b.ID, &b.Name, &b.IsActive, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return &b, nil
}

func (s *BrandStore) SearchByName(ctx context.Context, query string) ([]*model.Brand, error) {
	q := `SELECT id, name, is_active, created_at, updated_at FROM brands WHERE is_active = TRUE AND name ILIKE '%' || $1 || '%' ORDER BY name ASC LIMIT 20;`
	rows, err := s.db.QueryContext(ctx, q, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	brands := make([]*model.Brand, 0)
	for rows.Next() {
		b := new(model.Brand)
		if err := rows.Scan(&b.ID, &b.Name, &b.IsActive, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}

		brands = append(brands, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return brands, nil
}

func (s *BrandStore) SearchAllByName(ctx context.Context, query string) ([]*model.Brand, error) {
	q := `SELECT id, name, is_active, created_at, updated_at FROM brands WHERE name ILIKE '%' || $1 || '%' ORDER BY is_active DESC, name ASC LIMIT 20;`
	rows, err := s.db.QueryContext(ctx, q, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	brands := make([]*model.Brand, 0)
	for rows.Next() {
		b := new(model.Brand)
		if err := rows.Scan(&b.ID, &b.Name, &b.IsActive, &b.CreatedAt, &b.UpdatedAt); err != nil {
			return nil, err
		}

		brands = append(brands, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return brands, nil
}

func (s *BrandStore) Update(ctx context.Context, id int64, brand *model.Brand) (*model.Brand, error) {
	if brand == nil {
		return nil, errors.New("brand cannot be nil")
	}
	// No toca is_active: eso pasa solo por SetActive. Si lo escribiera, un
	// PUT de la API sin "is_active" (llega como false) desactivaría la marca.
	// RETURNING devuelve el is_active real.
	q := `UPDATE brands SET name = $1 WHERE id = $2 RETURNING id, is_active, created_at, updated_at;`

	err := s.db.QueryRowContext(ctx, q, brand.Name, id).Scan(&brand.ID, &brand.IsActive, &brand.CreatedAt, &brand.UpdatedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		if isUniqueViolation(err) {
			return nil, store.ErrDuplicateName
		}
		return nil, err
	}

	return brand, nil
}

func (s *BrandStore) Delete(ctx context.Context, id int64) error {
	q := `DELETE from brands WHERE id = $1;`

	res, err := s.db.ExecContext(ctx, q, id)
	if err != nil {
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

func (s *BrandStore) SetActive(ctx context.Context, id int64, active bool) error {
	q := `UPDATE brands SET is_active = $1 WHERE id = $2;`

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

func scanBrand(row rowScanner) (*model.Brand, error) {
	b := new(model.Brand)
	if err := row.Scan(&b.ID, &b.Name, &b.IsActive, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, err
	}
	return b, nil
}

func (s *BrandStore) ListAll(ctx context.Context, query string, limit int) ([]*model.Brand, int, error) {
	const where = `WHERE ($1 = '' OR name ILIKE '%' || $1 || '%')`
	return listPage(ctx, s.db,
		`SELECT count(*) FROM brands `+where+`;`,
		`SELECT id, name, is_active, created_at, updated_at FROM brands `+where+`
		 ORDER BY is_active DESC, name LIMIT $2;`,
		scanBrand, limit, query)
}
