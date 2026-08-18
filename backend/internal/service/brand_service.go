// Package service implements the core business logic, domain rules, and data validation.
package service

import (
	"errors"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

type BrandService struct {
	store store.BrandStore
}

func NewBrandService(s store.BrandStore) *BrandService {
	return &BrandService{
		store: s,
	}
}

func (s *BrandService) GetAllBrands() ([]*model.Brand, error) {
	return s.store.GetAll()
}

func (s *BrandService) GetBrandByID(id int64) (*model.Brand, error) {
	if id <= 0 {
		return nil, errors.New("invalid brand id")
	}
	return s.store.GetByID(id)
}

func (s *BrandService) CreateBrand(brand *model.Brand) (*model.Brand, error) {
	if err := s.validate(brand); err != nil {
		return nil, err
	}
	return s.store.Create(brand)
}

func (s *BrandService) UpdateBrand(id int64, brand *model.Brand) (*model.Brand, error) {
	if id <= 0 {
		return nil, errors.New("invalid brand id")
	}

	if err := s.validate(brand); err != nil {
		return nil, err
	}

	return s.store.Update(id, brand)
}

func (s *BrandService) DeleteBrand(id int64) error {
	if id <= 0 {
		return errors.New("invalid brand id")
	}
	return s.store.Delete(id)
}

func (s *BrandService) SearchByName(query string) ([]*model.Brand, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return []*model.Brand{}, nil
	}
	return s.store.SearchByName(q)
}

func (s *BrandService) validate(brand *model.Brand) error {
	if brand == nil {
		return errors.New("brand data is required")
	}

	brand.Name = strings.TrimSpace(brand.Name)
	if brand.Name == "" {
		return errors.New("brand name is required")
	}

	if len(brand.Name) < 2 {
		return errors.New("brand name must have at least 2 characters")
	}

	return nil
}
