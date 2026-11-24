package postgres_test

import (
	"context"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Igual que en marcas: Update no cambia is_active (ver brand_store_test.go).
func TestSupplierStore_UpdateKeepsIsActive(t *testing.T) {
	ctx := context.Background()
	s := postgres.NewSupplierStore(testTx(t))

	sp, err := s.Create(ctx, &model.Supplier{Name: "Proveedor test store"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	updated, err := s.Update(ctx, sp.ID, &model.Supplier{Name: "Proveedor test store 2", IsActive: false})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !updated.IsActive {
		t.Fatal("update deactivated the supplier")
	}

	if err := s.SetActive(ctx, sp.ID, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	updated, err = s.Update(ctx, sp.ID, &model.Supplier{Name: "Proveedor test store 3", IsActive: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.IsActive {
		t.Fatal("update reactivated the supplier")
	}
}
