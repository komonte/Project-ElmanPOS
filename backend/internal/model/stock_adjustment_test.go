package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

func TestStockAdjustmentValidate(t *testing.T) {
	a := &model.StockAdjustment{ProductID: 1, Reason: model.ReasonOwnConsumption, Quantity: dec("2"), Notes: "  mate  "}
	if err := a.Validate(); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if a.Notes != "mate" {
		t.Errorf("notes not trimmed: %q", a.Notes)
	}

	valid := func() *model.StockAdjustment {
		return &model.StockAdjustment{ProductID: 1, Reason: model.ReasonDamage, Quantity: dec("1")}
	}
	zero, batch := int64(0), int64(3)
	tests := []struct {
		name    string
		change  func(a *model.StockAdjustment)
		wantErr error
	}{
		{"no product", func(a *model.StockAdjustment) { a.ProductID = 0 }, model.ErrStockAdjustmentProduct},
		{"expired", func(a *model.StockAdjustment) { a.Reason = model.ReasonExpiration }, nil},
		{"count is not an exit reason", func(a *model.StockAdjustment) { a.Reason = model.ReasonCount }, model.ErrInvalidAdjustmentReason},
		{"theft is not offered", func(a *model.StockAdjustment) { a.Reason = "THEFT" }, model.ErrInvalidAdjustmentReason},
		{"empty reason", func(a *model.StockAdjustment) { a.Reason = "" }, model.ErrInvalidAdjustmentReason},
		{"zero quantity", func(a *model.StockAdjustment) { a.Quantity = dec("0") }, model.ErrInvalidAdjustmentQuantity},
		{"negative quantity", func(a *model.StockAdjustment) { a.Quantity = dec("-1") }, model.ErrInvalidAdjustmentQuantity},
		{"weight", func(a *model.StockAdjustment) { a.Quantity = dec("0.350") }, nil},
		{"4 decimals", func(a *model.StockAdjustment) { a.Quantity = dec("0.0001") }, model.ErrInvalidAdjustmentQuantity},
		{"notes of 500", func(a *model.StockAdjustment) { a.Notes = strings.Repeat("ñ", 500) }, nil},
		{"notes too long", func(a *model.StockAdjustment) { a.Notes = strings.Repeat("ñ", 501) }, model.ErrAdjustmentNotesTooLong},
		{"other without notes", func(a *model.StockAdjustment) { a.Reason, a.Notes = model.ReasonOther, "   " }, model.ErrAdjustmentNotesRequired},
		{"other with notes", func(a *model.StockAdjustment) { a.Reason, a.Notes = model.ReasonOther, "muestra" }, nil},
		{"batch chosen", func(a *model.StockAdjustment) { a.BatchID = &batch }, nil},
		{"bad batch", func(a *model.StockAdjustment) { a.BatchID = &zero }, model.ErrInvalidAdjustmentBatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := valid()
			tt.change(a)
			if err := a.Validate(); !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
	if err := (*model.StockAdjustment)(nil).Validate(); !errors.Is(err, model.ErrNilStockAdjustment) {
		t.Errorf("nil: got %v", err)
	}
}
