package service

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/shopspring/decimal"
)

var (
	// validations
	minTaxRate = decimal.Zero
	maxTaxRate = decimal.NewFromInt(100)
)

const (
	minTaxNameLength = 2
	maxTaxNameLength = 80
)

type TaxStore interface {
	Create(ctx context.Context, tax *model.Tax) (*model.Tax, error)
	GetAll(ctx context.Context) ([]*model.Tax, error)
	GetByID(ctx context.Context, id int64) (*model.Tax, error)
	SearchByName(ctx context.Context, query string) ([]*model.Tax, error)
	Update(ctx context.Context, id int64, tax *model.Tax) (*model.Tax, error)
	Delete(ctx context.Context, id int64) error
}

type TaxService struct {
	store TaxStore
}

func NewTaxService(s TaxStore) *TaxService {
	return &TaxService{
		store: s,
	}
}

func (s *TaxService) GetAllTaxes(ctx context.Context) ([]*model.Tax, error) {
	return s.store.GetAll(ctx)
}

func (s *TaxService) GetTaxByID(ctx context.Context, id int64) (*model.Tax, error) {
	if id <= 0 {
		return nil, ErrInvalidID
	}
	tax, err := s.store.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrTaxNotFound
		}
		return nil, err
	}

	return tax, nil
}

func (s *TaxService) CreateTax(ctx context.Context, tax *model.Tax) (*model.Tax, error) {
	if err := s.validate(tax); err != nil {
		return nil, err
	}

	created, err := s.store.Create(ctx, tax)
	if err != nil {
		if errors.Is(err, store.ErrDuplicateTaxName) {
			return nil, ErrTaxAlreadyExists
		}
		return nil, err
	}
	return created, nil
}

func (s *TaxService) UpdateTax(ctx context.Context, id int64, tax *model.Tax) (*model.Tax, error) {
	if id <= 0 {
		return nil, ErrInvalidID
	}

	if err := s.validate(tax); err != nil {
		return nil, err
	}

	tax.ID = id

	updated, err := s.store.Update(ctx, id, tax)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrTaxNotFound
		}
		if errors.Is(err, store.ErrDuplicateTaxName) {
			return nil, ErrTaxAlreadyExists
		}
		return nil, err
	}
	return updated, nil
}

func (s *TaxService) DeleteTax(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrInvalidID
	}

	if err := s.store.Delete(ctx, id); err != nil {
		// With these kind of verifications we make sure no sql type errors
		// are found in the http layer, instead we traduce them here.
		if errors.Is(err, store.ErrNotFound) {
			return ErrTaxNotFound
		}
		return err
	}
	return nil
}

func (s *TaxService) SearchByName(ctx context.Context, query string) ([]*model.Tax, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return []*model.Tax{}, nil
	}
	return s.store.SearchByName(ctx, q)
}

func (s *TaxService) validate(tax *model.Tax) error {
	if tax == nil {
		return ErrTaxRequired
	}

	tax.Name = strings.TrimSpace(tax.Name)
	if tax.Name == "" {
		return ErrInvalidName
	}

	if utf8.RuneCountInString(tax.Name) < minTaxNameLength {
		return ErrInvalidNameMinLen
	}

	if utf8.RuneCountInString(tax.Name) > maxTaxNameLength {
		return ErrInvalidNameMaxLen
	}

	if tax.Rate.LessThan(minTaxRate) {
		return ErrInvalidMinRate
	}

	if tax.Rate.GreaterThan(maxTaxRate) {
		return ErrInvalidMaxRate
	}
	return nil
}
