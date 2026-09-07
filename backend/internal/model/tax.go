package model

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

const (
	TaxNameMinLen = 2
	TaxNameMaxLen = 50
)

var (
	ErrNilTax       = errors.New("tax cannot be nil")

	ErrEmptyTaxName = errors.New("tax name is required")
	ErrTaxNameTooShort = errors.New("tax name must be at least 2 characters")
	ErrTaxNameTooLong  = errors.New("tax name cannot exceed 50 characters")

	ErrInvalidRate = errors.New("tax rate must be between 0 and 100")
)

var maxTaxRate = decimal.NewFromInt(100)

type Tax struct {
	ID   int64           `json:"id" db:"id"`
	Name string          `json:"name" db:"name"`
	Rate decimal.Decimal `json:"rate" db:"rate"`
}

func (t *Tax) Validate() error {
	if t == nil {
		return ErrNilTax
	}
	t.Name = strings.TrimSpace(t.Name)
	if t.Name == "" {
		return ErrEmptyTaxName
	}

	nameLen := utf8.RuneCountInString(t.Name)
	if nameLen < TaxNameMinLen {
		return ErrTaxNameTooShort
	}
	if nameLen > TaxNameMaxLen {
		return ErrTaxNameTooLong
	}

	if t.Rate.IsNegative() || t.Rate.GreaterThan(maxTaxRate) {
		return ErrInvalidRate
	}
	return nil
}
