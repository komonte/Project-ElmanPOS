// Package service implements the core business logic, domain rules, and data validation.
package service

import (
	"context"
	"errors"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

type BrandStore interface {
	Create(ctx context.Context, brand *model.Brand) (*model.Brand, error)
	GetAll(ctx context.Context) ([]*model.Brand, error)
	GetByID(ctx context.Context, id int64) (*model.Brand, error)
	SearchByName(ctx context.Context, query string) ([]*model.Brand, error)
	Update(ctx context.Context, id int64, brand *model.Brand) (*model.Brand, error)
	Delete(ctx context.Context, id int64) error
}

type BrandService struct {
	store BrandStore
}

func NewBrandService(s BrandStore) *BrandService {
	return &BrandService{
		store: s,
	}
}

func (s *BrandService) GetAllBrands(ctx context.Context) ([]*model.Brand, error) {
	return s.store.GetAll(ctx)
}

func (s *BrandService) GetBrandByID(ctx context.Context, id int64) (*model.Brand, error) {
	if id <= 0 {
		return nil, ErrInvalidID
	}

	brand, err := s.store.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrBrandNotFound
		}
		return nil, err
	}
	return brand, nil
}

func (s *BrandService) CreateBrand(ctx context.Context, brand *model.Brand) (*model.Brand, error) {
	if err := brand.Validate(); err != nil {
		return nil, err
	}

	created, err := s.store.Create(ctx, brand)
	if err != nil {
		if errors.Is(err, store.ErrDuplicateBrandName) {
			return nil, ErrBrandAlreadyExists
		}
		return nil, err
	}
	return created, nil
}

func (s *BrandService) UpdateBrand(ctx context.Context, id int64, brand *model.Brand) (*model.Brand, error) {
	if id <= 0 {
		return nil, ErrInvalidID
	}
	if err := brand.Validate(); err != nil {
		return nil, err
	}

	updated, err := s.store.Update(ctx, id, brand)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrBrandNotFound
		}
		if errors.Is(err, store.ErrDuplicateBrandName) {
			return nil, ErrBrandAlreadyExists
		}
		return nil, err
	}
	return updated, nil
}

func (s *BrandService) DeleteBrand(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrInvalidID
	}

	if err := s.store.Delete(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrBrandNotFound
		}
		return err
	}
	return nil
}

func (s *BrandService) SearchByName(ctx context.Context, query string) ([]*model.Brand, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return []*model.Brand{}, nil
	}
	return s.store.SearchByName(ctx, q)
}
