package store

import (
	"database/sql"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

type Store interface {
	Create(brand *model.Brand) (*model.Brand, error)
	GetAll() ([]*model.Brand, error)
	GetByID(id int64) (*model.Brand, error)
	SearchByName(query string) ([]*model.Brand, error)
	Update(id int64, brand *model.Brand) (*model.Brand, error)
	Delete(id int64) error
}

type store struct {
	db *sql.DB
}

func New(db *sql.DB) Store {
	return &store{db: db}
}

func (s *store) Create(brand *model.Brand) (*model.Brand, error) {
	q := `INSERT INTO brands (name) VALUES ($1) RETURNING id;`
	err := s.db.QueryRow(q, brand.Name).Scan(&brand.ID)
	if err != nil {
		return nil, err
	}

	return brand, nil
}

func (s *store) GetAll() ([]*model.Brand, error) {
	q := `SELECT id, name FROM brands;`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var brands []*model.Brand
	for rows.Next() {
		var b model.Brand
		if err := rows.Scan(&b.ID, &b.Name); err != nil {
			return nil, err
		}

		brands = append(brands, &b)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return brands, nil
}

func (s *store) GetByID(id int64) (*model.Brand, error) {
	q := `SELECT id, name FROM brands WHERE id = $1;`

	var b model.Brand

	err := s.db.QueryRow(q, id).Scan(&b.ID, &b.Name)
	if err != nil {
		return nil, err
	}

	return &b, nil
}

func (s *store) SearchByName(query string) ([]*model.Brand, error) {
	q := `SELECT id, name FROM brands WHERE name ILIKE '%' || $1 || '%' ORDER BY name ASC LIMIT 20;`
	rows, err := s.db.Query(q, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var brands []*model.Brand
	for rows.Next() {
		b := new(model.Brand)
		if err := rows.Scan(&b.ID, &b.Name); err != nil {
			return nil, err
		}

		brands = append(brands, b)
	}

	return brands, nil
}

func (s *store) Update(id int64, brand *model.Brand) (*model.Brand, error) {
	q := `UPDATE brands SET name = $1 WHERE id = $2;`

	_, err := s.db.Exec(q, brand.Name, id)
	if err != nil {
		return nil, err
	}

	brand.ID = id

	return brand, nil
}

func (s *store) Delete(id int64) error {
	q := `DELETE from brands WHERE id = $1;`

	_, err := s.db.Exec(q, id)
	if err != nil {
		return err
	}

	return nil
}
