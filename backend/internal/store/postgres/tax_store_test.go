package postgres_test

import (
	"context"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
	"github.com/shopspring/decimal"
)

// Igual que en marcas: Update no cambia is_active (ver brand_store_test.go).
func TestTaxStore_UpdateKeepsIsActive(t *testing.T) {
	ctx := context.Background()
	s := postgres.NewTaxStore(testTx(t))
	rate := decimal.NewFromInt(21)

	tax, err := s.Create(ctx, &model.Tax{Name: "IVA test store", Rate: rate})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := s.Update(ctx, tax.ID, &model.Tax{Name: "IVA test store 2", Rate: rate, IsActive: false})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !updated.IsActive {
		t.Fatal("update deactivated the tax")
	}

	if err := s.SetActive(ctx, tax.ID, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	updated, err = s.Update(ctx, tax.ID, &model.Tax{Name: "IVA test store 3", Rate: rate, IsActive: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.IsActive {
		t.Fatal("update reactivated the tax")
	}
}
