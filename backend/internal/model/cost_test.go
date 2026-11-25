package model_test

import (
	"errors"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

func TestCostValidate(t *testing.T) {
	tests := []struct {
		name    string
		cost    string
		wantErr error
	}{
		{name: "whole cost", cost: "700"},
		{name: "two decimals", cost: "699.99"},
		{name: "trailing zeros are not extra decimals", cost: "10.500"},
		{name: "smallest cost", cost: "0.01"},
		{name: "largest cost", cost: "9999999999.99"},
		{name: "zero", cost: "0", wantErr: model.ErrInvalidCost},
		{name: "negative", cost: "-10", wantErr: model.ErrInvalidCost},
		{name: "three decimals", cost: "10.555", wantErr: model.ErrInvalidCost},
		{name: "too large", cost: "10000000000", wantErr: model.ErrInvalidCost},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &model.Cost{ProductID: 1, UnitCost: dec(tt.cost)}
			if err := c.Validate(); !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCostValidateNil(t *testing.T) {
	var c *model.Cost
	if err := c.Validate(); !errors.Is(err, model.ErrNilCost) {
		t.Errorf("got %v, want %v", err, model.ErrNilCost)
	}
}

func TestAverageCost(t *testing.T) {
	prev := &model.Cost{UnitCost: dec("100"), AvgCost: dec("100")}
	tests := []struct {
		name     string
		prev     *model.Cost
		onHand   string
		unitCost string
		qtyIn    string
		want     string
	}{
		{name: "first cost, manual", prev: nil, onHand: "0", unitCost: "120", qtyIn: "0", want: "120"},
		{name: "first cost, purchase", prev: nil, onHand: "0", unitCost: "120", qtyIn: "10", want: "120"},
		{name: "first cost with stock (seed or count)", prev: nil, onHand: "5", unitCost: "120", qtyIn: "0", want: "120"},
		{name: "no stock: average restarts", prev: prev, onHand: "0", unitCost: "130", qtyIn: "10", want: "130"},
		{name: "manual with stock keeps average", prev: prev, onHand: "10", unitCost: "130", qtyIn: "0", want: "100"},
		{name: "purchase averages", prev: prev, onHand: "10", unitCost: "130", qtyIn: "10", want: "115"},
		{name: "weighted by quantity", prev: prev, onHand: "30", unitCost: "200", qtyIn: "10", want: "125"},
		{name: "cost drop lowers average", prev: prev, onHand: "10", unitCost: "70", qtyIn: "20", want: "80"},
		// (100 × 3 + 110 × 4) / 7 = 105.714… → 105.71
		{name: "rounds to 2 decimals", prev: prev, onHand: "3", unitCost: "110", qtyIn: "4", want: "105.71"},
		// Por peso: 2,5 kg a 100 + 1,25 kg a 160 = 450 / 3,75 = 120
		{name: "fractional quantities (kg)", prev: prev, onHand: "2.5", unitCost: "160", qtyIn: "1.25", want: "120"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := model.AverageCost(tt.prev, dec(tt.onHand), dec(tt.unitCost), dec(tt.qtyIn))
			if !got.Equal(dec(tt.want)) {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSuggestPrice(t *testing.T) {
	tests := []struct {
		name   string
		cost   string
		margin string
		want   string
		wantOK bool
	}{
		{name: "30% margin", cost: "700", margin: "30", want: "1000", wantOK: true},
		{name: "50% margin doubles", cost: "450.50", margin: "50", want: "901", wantOK: true},
		// 1000 / 0,7 = 1428,571… → 1428,57
		{name: "rounds to 2 decimals", cost: "1000", margin: "30", want: "1428.57", wantOK: true},
		{name: "decimal margin", cost: "100", margin: "37.5", want: "160", wantOK: true},
		{name: "margin 0: no suggestion", cost: "700", margin: "0"},
		{name: "margin 100: no suggestion", cost: "700", margin: "100"},
		{name: "negative margin: no suggestion", cost: "700", margin: "-5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := model.SuggestPrice(dec(tt.cost), dec(tt.margin))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && !got.Equal(dec(tt.want)) {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}
