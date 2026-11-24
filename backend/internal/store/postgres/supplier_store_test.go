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

// Los opcionales vacíos se guardan como NULL. Con '' el segundo proveedor
// sin CUIT chocaba con el primero en el UNIQUE (dos '' son iguales; dos NULL no).
func TestSupplierStore_CreateEmptyOptionalsAreNull(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	s := postgres.NewSupplierStore(tx)

	a, err := s.Create(ctx, &model.Supplier{Name: "Proveedor sin CUIT A"})
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	if _, err := s.Create(ctx, &model.Supplier{Name: "Proveedor sin CUIT B"}); err != nil {
		t.Fatalf("create B (second supplier without CUIT): %v", err)
	}

	var nulls int
	err = tx.QueryRowContext(ctx, `
		SELECT num_nulls(tax_id_number, phone, email, payment_terms)
		FROM suppliers WHERE id = $1;`, a.ID).Scan(&nulls)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if nulls != 4 {
		t.Fatalf("got %d NULL optionals, want 4", nulls)
	}

	// Leído de vuelta, NULL vuelve como "".
	got, err := s.GetByID(ctx, a.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.TaxIDNumber != "" || got.Email != "" {
		t.Fatalf("got %+v, want empty optionals", got)
	}
}
