package service

// TODO
// Create tax service and its unit test

import (
	"errors"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/shopspring/decimal"
)

const (
	minTaxNameLenght = 2
	maxTaxNameLenght = 50
)

var (
    minTaxRate = decimal.Zero
    maxTaxRate = decimal.NewFromInt(100)
)

type TaxService struct {
	store store.TaxStore
}

func NewTaxService(s store.TaxStore) *TaxService {
	return &TaxService{
		store: s,
	}
}


// replace brand with tax and make sure all business rules are declared
func (s *TaxService) GetAllTaxes() ([]*model.Tax, error) {
	return s.store.GetAll()
}

func (s *TaxService) GetTaxByID(id int64) (*model.Tax, error) {
	if id <= 0 {
		return nil, errors.New("invalid tax id")
	}
	return s.store.GetByID(id)
}

func (s *TaxService) CreateTax(tax *model.Tax) (*model.Tax, error) {
	if err := s.validate(tax); err != nil {
		return nil, err
	}
	return s.store.Create(tax)
}

func (s *TaxService) UpdateTax(id int64, tax *model.Tax) (*model.Tax, error) {
	if id <= 0 {
		return nil, errors.New("invalid tax id")
	}

	if err := s.validate(tax); err != nil {
		return nil, err
	}

	tax.ID = id
	return s.store.Update(id, tax)
}

func (s *TaxService) DeleteTax(id int64) error {
	if id <= 0 {
		return errors.New("invalid tax id")
	}
	return s.store.Delete(id)
}

func (s *TaxService) SearchByName(query string) ([]*model.Tax, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return []*model.Tax{}, nil
	}
	return s.store.SearchByName(q)
}


func (s *TaxService) validate(tax *model.Tax) error {
	if tax == nil {
		return errors.New("tax data is required")
	}

	tax.Name = strings.TrimSpace(tax.Name)
	if tax.Name == "" {
		return errors.New("tax name is required")
	}

	if len(tax.Name) < minTaxNameLenght {
		return errors.New("tax name must have at least 2 characters")
	}

	if len(tax.Name) > maxTaxNameLenght {
		return errors.New("tax name must have less than 50 characters")
	}

	if tax.Rate.LessThan(minTaxRate) {
		return errors.New("tax rate must be greater than zero")
	}

	if tax.Rate.GreaterThan(maxTaxRate) {
		return errors.New("tax rate cannot exceed 100%")
	}
	return nil
}
