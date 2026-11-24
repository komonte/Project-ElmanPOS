package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/shopspring/decimal"
)

func TestTaxValidate(t *testing.T) {
	rate := func(s string) decimal.Decimal { return decimal.RequireFromString(s) }

	tests := []struct {
		name     string
		tax      model.Tax
		wantErr  error
		wantName string
	}{
		{name: "IVA 21%", tax: model.Tax{Name: " IVA 21% ", Rate: rate("21")}, wantName: "IVA 21%"},
		{name: "inner spaces are tidied", tax: model.Tax{Name: "IVA   21%", Rate: rate("21")}, wantName: "IVA 21%"},
		{name: "decimal rate", tax: model.Tax{Name: "IVA 10,5%", Rate: rate("10.5")}, wantName: "IVA 10,5%"},
		{name: "rate 0 (exento)", tax: model.Tax{Name: "Exento", Rate: rate("0")}, wantName: "Exento"},
		{name: "rate 100", tax: model.Tax{Name: "Tope", Rate: rate("100")}, wantName: "Tope"},
		{name: "negative rate", tax: model.Tax{Name: "IVA", Rate: rate("-0.01")}, wantErr: model.ErrInvalidRate},
		{name: "rate over 100", tax: model.Tax{Name: "IVA", Rate: rate("100.01")}, wantErr: model.ErrInvalidRate},
		{name: "empty name", tax: model.Tax{Name: "  ", Rate: rate("21")}, wantErr: model.ErrEmptyTaxName},
		{name: "name too short", tax: model.Tax{Name: "I", Rate: rate("21")}, wantErr: model.ErrTaxNameTooShort},
		{name: "name too long", tax: model.Tax{Name: strings.Repeat("a", model.TaxNameMaxLen+1), Rate: rate("21")}, wantErr: model.ErrTaxNameTooLong},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tax := tt.tax
			err := tax.Validate()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && tax.Name != tt.wantName {
				t.Fatalf("name: got %q, want %q", tax.Name, tt.wantName)
			}
		})
	}
}

func TestTaxValidateNil(t *testing.T) {
	var tax *model.Tax
	if err := tax.Validate(); !errors.Is(err, model.ErrNilTax) {
		t.Fatalf("error: got %v, want ErrNilTax", err)
	}
}
