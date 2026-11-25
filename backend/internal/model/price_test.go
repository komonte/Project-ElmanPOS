package model_test

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

func TestPriceValidate(t *testing.T) {
	tests := []struct {
		name    string
		price   string
		wantErr error
	}{
		{name: "whole price", price: "1500"},
		{name: "two decimals", price: "1499.99"},
		{name: "trailing zeros are not extra decimals", price: "10.500"},
		{name: "smallest price", price: "0.01"},
		{name: "largest price", price: "9999999999.99"},
		{name: "zero", price: "0", wantErr: model.ErrInvalidPrice},
		{name: "negative", price: "-10", wantErr: model.ErrInvalidPrice},
		{name: "three decimals", price: "10.555", wantErr: model.ErrInvalidPrice},
		{name: "too large", price: "10000000000", wantErr: model.ErrInvalidPrice},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &model.Price{ProductID: 1, UnitPrice: decimal.RequireFromString(tt.price)}
			if err := p.Validate(); !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestPriceValidateNil(t *testing.T) {
	var p *model.Price
	if err := p.Validate(); !errors.Is(err, model.ErrNilPrice) {
		t.Errorf("got %v, want %v", err, model.ErrNilPrice)
	}
}
