package store

import (
	"context"
	"database/sql"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

type TaxStore interface {
	Create(ctx context.Context, tax *model.Tax) (*model.Tax, error)
	GetAll(ctx context.Context) ([]*model.Tax, error)
	GetByID(ctx context.Context, id int64) (*model.Tax, error)
	SearchByName(ctx context.Context, query string) ([]*model.Tax, error)
	Update(ctx context.Context, id int64, tax *model.Tax) (*model.Tax, error)
	Delete(ctx context.Context, id int64) error
}

type taxStore struct {
	db *sql.DB
}

func NewTaxStore(db *sql.DB) TaxStore {
	return &taxStore{db: db}
}

// Create all functions to fulfill the interface
func (s *taxStore) Create(ctx context.Context, tax *model.Tax) (*model.Tax, error) {
	q := `INSERT INTO taxes(name, rate) VALUES ($1, $2) RETURNING id;`
	err := s.db.QueryRowContext(ctx, q, tax.Name, tax.Rate).Scan(&tax.ID)
	if err != nil {
		return nil, err
	}
	return tax, nil
}

func (s *taxStore) GetAll(ctx context.Context) ([]*model.Tax, error) {
	q := `SELECT id, name, rate FROM taxes;`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	taxes := make([]*model.Tax, 0)
	for rows.Next() {
		t := new(model.Tax)
		if err := rows.Scan(&t.ID, &t.Name, &t.Rate); err != nil {
			return nil, err
		}

		taxes = append(taxes, t)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return taxes, nil
}

func (s *taxStore) GetByID(ctx context.Context, id int64) (*model.Tax, error) {
	q := `SELECT id, name, rate FROM taxes WHERE id = $1;`

	var t model.Tax

	err := s.db.QueryRowContext(ctx, q, id).Scan(&t.ID, &t.Name, &t.Rate)
	if err != nil {
		return nil, err
	}

	return &t, nil
}

func (s *taxStore) SearchByName(ctx context.Context, query string) ([]*model.Tax, error) {
	q := `SELECT id, name, rate FROM taxes WHERE name ILIKE '%' || $1 || '%' ORDER BY name ASC LIMIT 20;`
	rows, err := s.db.QueryContext(ctx, q, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	taxes := make([]*model.Tax, 0)
	for rows.Next() {
		t := new(model.Tax)
		if err := rows.Scan(&t.ID, &t.Name, &t.Rate); err != nil {
			return nil, err
		}

		taxes = append(taxes, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return taxes, nil
}

func (s *taxStore) Update(ctx context.Context, id int64, tax *model.Tax) (*model.Tax, error) {
	q := `UPDATE taxes SET name = $1, rate = $2 WHERE id = $3;`

	res, err := s.db.ExecContext(ctx, q, tax.Name, tax.Rate, id)
	if err != nil {
		return nil, err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}

	if rows == 0 {
		return nil, sql.ErrNoRows
	}

	tax.ID = id

	return tax, nil
}

func (s *taxStore) Delete(ctx context.Context, id int64) error {
	q := `DELETE from taxes WHERE id = $1;`

	res, err := s.db.ExecContext(ctx, q, id)
	if err != nil {
		return err
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return sql.ErrNoRows
	}

	return nil
}
