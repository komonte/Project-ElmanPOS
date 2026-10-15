package service

import (
	"context"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

type TaxService struct {
	generic *Service[model.Tax]
	store   *postgres.TaxStore
}

func NewTaxService(generic *Service[model.Tax], store *postgres.TaxStore) *TaxService {
	return &TaxService{generic: generic, store: store}
}

func (s *TaxService) GetAll(ctx context.Context) ([]*model.Tax, error) {
	return s.generic.GetAll(ctx)
}

func (s *TaxService) GetAllIncludingInactive(ctx context.Context) ([]*model.Tax, error) {
	return s.store.GetAllIncludingInactive(ctx)
}

func (s *TaxService) GetByID(ctx context.Context, id int64) (*model.Tax, error) {
	return s.generic.GetByID(ctx, id)
}

func (s *TaxService) Create(ctx context.Context, entity *model.Tax) (*model.Tax, error) {
	return s.generic.Create(ctx, entity)
}

func (s *TaxService) Update(ctx context.Context, id int64, entity *model.Tax) (*model.Tax, error) {
	return s.generic.Update(ctx, id, entity)
}

func (s *TaxService) Delete(ctx context.Context, id int64) error {
	return s.generic.Delete(ctx, id)
}

func (s *TaxService) SearchByName(ctx context.Context, query string) ([]*model.Tax, error) {
	return s.generic.SearchByName(ctx, query)
}

func (s *TaxService) SearchAllByName(ctx context.Context, query string) ([]*model.Tax, error) {
	return s.store.SearchAllByName(ctx, query)
}

func (s *TaxService) Deactivate(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrInvalidID
	}
	return s.store.SetActive(ctx, id, false)
}

func (s *TaxService) Reactivate(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrInvalidID
	}
	return s.store.SetActive(ctx, id, true)
}