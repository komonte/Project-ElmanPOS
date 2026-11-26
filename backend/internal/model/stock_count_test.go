package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

func TestStockCountEntryValidate(t *testing.T) {
	e := &model.StockCountEntry{ProductID: 1, Sector: "  Góndola   3 ", Quantity: dec("12")}
	if err := e.Validate(); err != nil {
		t.Fatalf("valid entry: %v", err)
	}
	if e.Sector != "Góndola 3" {
		t.Errorf("sector not tidied: %q", e.Sector)
	}

	tests := []struct {
		name    string
		entry   *model.StockCountEntry
		wantErr error
	}{
		{"nil", nil, model.ErrNilStockCountEntry},
		{"no product", &model.StockCountEntry{Sector: "Depósito", Quantity: dec("1")}, model.ErrStockCountEntryProduct},
		{"empty sector", &model.StockCountEntry{ProductID: 1, Sector: "   ", Quantity: dec("1")}, model.ErrStockCountSectorEmpty},
		{"sector of 60 letters", &model.StockCountEntry{ProductID: 1, Sector: strings.Repeat("ñ", 60), Quantity: dec("1")}, nil},
		{"sector too long", &model.StockCountEntry{ProductID: 1, Sector: strings.Repeat("ñ", 61), Quantity: dec("1")}, model.ErrStockCountSectorLong},
		{"zero is a count", &model.StockCountEntry{ProductID: 1, Sector: "Depósito", Quantity: dec("0")}, nil},
		{"weight", &model.StockCountEntry{ProductID: 1, Sector: "Heladera", Quantity: dec("2.345")}, nil},
		{"negative", &model.StockCountEntry{ProductID: 1, Sector: "Depósito", Quantity: dec("-1")}, model.ErrInvalidCountedQuantity},
		{"4 decimals", &model.StockCountEntry{ProductID: 1, Sector: "Depósito", Quantity: dec("1.0001")}, model.ErrInvalidCountedQuantity},
		{"too large", &model.StockCountEntry{ProductID: 1, Sector: "Depósito", Quantity: dec("1000000000")}, model.ErrInvalidCountedQuantity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.entry.Validate(); !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestStockCountItemValidate(t *testing.T) {
	it := &model.StockCountItem{UnitCost: decPtr("850.50"), ExpiryDate: " 2027-03-31 "}
	if err := it.Validate(); err != nil {
		t.Fatalf("valid item: %v", err)
	}
	if it.ExpiryDate != "2027-03-31" {
		t.Errorf("expiry not trimmed: %q", it.ExpiryDate)
	}

	tests := []struct {
		name    string
		item    *model.StockCountItem
		wantErr error
	}{
		{"nil", nil, model.ErrNilStockCountItem},
		{"no cost and no expiry", &model.StockCountItem{}, nil},
		{"zero cost", &model.StockCountItem{UnitCost: decPtr("0")}, model.ErrInvalidCost},
		{"cost with 3 decimals", &model.StockCountItem{UnitCost: decPtr("1.005")}, model.ErrInvalidCost},
		{"bad expiry", &model.StockCountItem{ExpiryDate: "31/03/2027"}, model.ErrInvalidExpiryDate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.item.Validate(); !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestStockCountItemDifference(t *testing.T) {
	tests := []struct{ system, counted, want string }{
		{"10", "7", "-3"},
		{"0", "24", "24"}, // inventario inicial
		{"1.250", "1.250", "0"},
		{"-2", "0", "2"}, // un saldo negativo vuelve a 0
	}
	for _, tt := range tests {
		it := &model.StockCountItem{SystemQuantity: dec(tt.system), CountedQuantity: dec(tt.counted)}
		if got := it.Difference(); !got.Equal(dec(tt.want)) {
			t.Errorf("counted %s, system %s: got %s, want %s", tt.counted, tt.system, got, tt.want)
		}
	}
}
