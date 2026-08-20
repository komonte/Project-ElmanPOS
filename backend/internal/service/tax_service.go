package service

// TODO
// Create tax service and its unit test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/shopspring/decimal"
)

var (
	// sentinel errors
	ErrTaxNotFound       = errors.New("tax not found")
	ErrTaxRequired       = errors.New("tax data is required")
	ErrInvalidID         = errors.New("invalid tax id")
	ErrInvalidName       = errors.New("tax name is required")
	ErrInvalidNameMinLen = errors.New("tax name must have at least 2 characters")
	ErrInvalidNameMaxLen = errors.New("tax name must have less than 80 characters")
	ErrInvalidMinRate    = errors.New("tax rate cannot be negative")
	ErrInvalidMaxRate    = errors.New("tax rate cannot exceed 100%")

	// validations
	minTaxRate = decimal.Zero
	maxTaxRate = decimal.NewFromInt(100)
)

const (
	minTaxNameLength = 2
	maxTaxNameLength = 80
)

type TaxService struct {
	store store.TaxStore
}

func NewTaxService(s store.TaxStore) *TaxService {
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
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTaxNotFound
	}

	return tax, err
}

func (s *TaxService) CreateTax(ctx context.Context, tax *model.Tax) (*model.Tax, error) {
	if err := s.validate(tax); err != nil {
		return nil, err
	}
	return s.store.Create(ctx, tax)
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
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTaxNotFound
	}
	return updated, err
}

func (s *TaxService) DeleteTax(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrInvalidID
	}

	err := s.store.Delete(ctx, id)

	// With these kind of verifications we make sure no sql type errors 
	// are found in the http layer, instead we traduce them here.
	if errors.Is(err, sql.ErrNoRows) {
		return ErrTaxNotFound
	}
	return err
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
