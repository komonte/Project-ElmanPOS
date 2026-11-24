package postgres_test

import (
	"context"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Update no cambia is_active: un PUT de la API sin "is_active" llega con
// false (el valor cero) y desactivaba la marca. Activar y desactivar pasan
// solo por SetActive.
func TestBrandStore_UpdateKeepsIsActive(t *testing.T) {
	ctx := context.Background()
	s := postgres.NewBrandStore(testTx(t))

	b, err := s.Create(ctx, &model.Brand{Name: "MARCA TEST STORE"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Activa + IsActive false en el pedido: sigue activa.
	updated, err := s.Update(ctx, b.ID, &model.Brand{Name: "MARCA TEST STORE 2", IsActive: false})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !updated.IsActive {
		t.Fatal("update deactivated the brand")
	}

	// Inactiva + IsActive true en el pedido: sigue inactiva.
	if err := s.SetActive(ctx, b.ID, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	updated, err = s.Update(ctx, b.ID, &model.Brand{Name: "MARCA TEST STORE 3", IsActive: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.IsActive {
		t.Fatal("update reactivated the brand")
	}
}
