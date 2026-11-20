package service

import (
	"context"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

type BrandService struct {
	generic *Service[model.Brand]
	store   *postgres.BrandStore
}

func NewBrandService(generic *Service[model.Brand], store *postgres.BrandStore) *BrandService {
	return &BrandService{generic: generic, store: store}
}

func (s *BrandService) GetAll(ctx context.Context) ([]*model.Brand, error) {
	return s.generic.GetAll(ctx)
}

func (s *BrandService) GetAllIncludingInactive(ctx context.Context) ([]*model.Brand, error) {
	return s.store.GetAllIncludingInactive(ctx)
}

func (s *BrandService) GetByID(ctx context.Context, id int64) (*model.Brand, error) {
	return s.generic.GetByID(ctx, id)
}

func (s *BrandService) Create(ctx context.Context, entity *model.Brand) (*model.Brand, error) {
	return s.generic.Create(ctx, entity)
}

func (s *BrandService) Update(ctx context.Context, id int64, entity *model.Brand) (*model.Brand, error) {
	return s.generic.Update(ctx, id, entity)
}

func (s *BrandService) Delete(ctx context.Context, id int64) error {
	return s.generic.Delete(ctx, id)
}

func (s *BrandService) SearchByName(ctx context.Context, query string) ([]*model.Brand, error) {
	return s.generic.SearchByName(ctx, query)
}

func (s *BrandService) SearchAllByName(ctx context.Context, query string) ([]*model.Brand, error) {
	return s.store.SearchAllByName(ctx, query)
}

func (s *BrandService) Deactivate(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrInvalidID
	}
	return s.store.SetActive(ctx, id, false)
}

func (s *BrandService) Reactivate(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrInvalidID
	}
	return s.store.SetActive(ctx, id, true)
}