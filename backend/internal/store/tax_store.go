package store

import (
	"database/sql"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

type TaxStore interface {
	Create(tax *model.Tax) (*model.Tax, error)
	GetAll() ([]*model.Tax, error)
	GetByID(id int64) (*model.Tax, error)
	SearchByName(query string) ([]*model.Tax, error)
	Update(id int64, brand *model.Tax) (*model.Tax, error)
	Delete(id int64) error
}

type taxStore struct {
	db *sql.DB
}

func NewTaxStore(db *sql.DB) TaxStore {
	return &taxStore{db: db}
}

// Create all functions to fulfill the interface
//
func (s *taxStore) Create(tax *model.Tax) (*model.Tax, error) {
	q := `INSERT INTO taxes(name, rate) VALUES ($1, $2) RETURNING id;`
	err := s.db.QueryRow(q, tax.Name, tax.Rate).Scan(&tax.ID)
	if err != nil {
		return nil, err
	}
	return tax, nil
}

func (s *taxStore) GetAll() ([]*model.Tax, error) {
	q := `SELECT id, name, rate FROM taxes;`

	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	taxes := make([]*model.Tax, 0)
	for rows.Next() {
		t := new(model.Tax)
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}

		taxes = append(taxes, t)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return taxes, nil
}

func (s *taxStore) GetByID(id int64) (*model.Tax, error) {
	q := `SELECT id, name, rate FROM taxes WHERE id = $1;`

	var t model.Tax

	err := s.db.QueryRow(q, id).Scan(&t.ID, &t.Name)
	if err != nil {
		return nil, err
	}

	return &t, nil
}

func (s *taxStore) SearchByName(query string) ([]*model.Tax, error) {
	q := `SELECT id, name, rate FROM taxes WHERE name ILIKE '%' || $1 || '%' ORDER BY name ASC LIMIT 20;`
	rows, err := s.db.Query(q, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var taxes []*model.Tax
	for rows.Next() {
		t := new(model.Tax)
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, err
		}

		taxes = append(taxes, t)
	}
	return taxes, nil
}

func (s *taxStore) Update(id int64, tax *model.Tax) (*model.Tax, error) {
	q := `UPDATE taxes SET name = $1, rate = $2 WHERE id = $3;`

	res, err := s.db.Exec(q, tax.Name, tax.Rate, id)
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

func (s *taxStore) Delete(id int64) error {
	q := `DELETE from taxes WHERE id = $1;`

	res, err := s.db.Exec(q, id)
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
