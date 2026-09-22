package postgres

import (
	"context"
	"database/sql"
	"errors"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

type BrandStore struct {
	db *sql.DB
}

func NewBrandStore(db *sql.DB) *BrandStore {
	return &BrandStore{db: db}
}

func (s *BrandStore) Create(ctx context.Context, brand *model.Brand) (*model.Brand, error) {
	if brand == nil {
		return nil, errors.New("brand cannot be nil")
	}

	q := `INSERT INTO brands (name) VALUES ($1) RETURNING id;`
	err := s.db.QueryRowContext(ctx, q, brand.Name).Scan(&brand.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, store.ErrDuplicateBrandName
		}
		return nil, err
	}
	return brand, nil
}

func (s *BrandStore) GetAll(ctx context.Context) ([]*model.Brand, error) {
	q := `SELECT id, name FROM brands;`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	brands := make([]*model.Brand, 0)
	for rows.Next() {
		b := new(model.Brand)
		if err := rows.Scan(&b.ID, &b.Name); err != nil {
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
	q := `SELECT id, name FROM brands WHERE id = $1;`

	var b model.Brand

	err := s.db.QueryRowContext(ctx, q, id).Scan(&b.ID, &b.Name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}

	return &b, nil
}

func (s *BrandStore) SearchByName(ctx context.Context, query string) ([]*model.Brand, error) {
	q := `SELECT id, name FROM brands WHERE name ILIKE '%' || $1 || '%' ORDER BY name ASC LIMIT 20;`
	rows, err := s.db.QueryContext(ctx, q, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	brands := make([]*model.Brand, 0)
	for rows.Next() {
		b := new(model.Brand)
		if err := rows.Scan(&b.ID, &b.Name); err != nil {
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
	q := `UPDATE brands SET name = $1 WHERE id = $2;`

	res, err := s.db.ExecContext(ctx, q, brand.Name, id)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, store.ErrDuplicateBrandName
		}
		return nil, err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}

	if rows == 0 {
		return nil, store.ErrNotFound
	}

	return brand, nil
}

func (s *BrandStore) Delete(ctx context.Context, id int64) error {
	q := `DELETE from brands WHERE id = $1;`

	res, err := s.db.ExecContext(ctx, q, id)
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
