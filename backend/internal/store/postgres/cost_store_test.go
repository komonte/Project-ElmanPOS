package postgres_test

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

func cost(productID int64, unit, avg string) *model.Cost {
	return &model.Cost{
		ProductID: productID,
		UnitCost:  decimal.RequireFromString(unit),
		AvgCost:   decimal.RequireFromString(avg),
	}
}

func TestCostStore_CreateAndCurrent(t *testing.T) {
	f := newProductFixture(t)
	costs := postgres.NewCostStore(f.tx)
	p := f.create(t, "TEST-CO-1", "COSTO TEST")

	got, err := costs.Create(f.ctx, cost(p.ID, "700.50", "650.25"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.ID == 0 || got.ProductID != p.ID ||
		!got.UnitCost.Equal(decimal.RequireFromString("700.50")) ||
		!got.AvgCost.Equal(decimal.RequireFromString("650.25")) ||
		got.ValidFrom == "" || got.CreatedAt == "" {
		t.Errorf("create: got %+v", got)
	}

	cur, err := costs.Current(f.ctx, p.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if cur.ID != got.ID || !cur.AvgCost.Equal(got.AvgCost) {
		t.Errorf("current: got %+v, want %+v", cur, got)
	}
}

// Dos costos en la misma transacción no chocan (clock_timestamp): pasa en una
// compra con dos renglones del mismo producto.
func TestCostStore_NewCostReplacesCurrent(t *testing.T) {
	f := newProductFixture(t)
	costs := postgres.NewCostStore(f.tx)
	p := f.create(t, "TEST-CO-2", "CAMBIO COSTO TEST")

	first, err := costs.Create(f.ctx, cost(p.ID, "100", "100"))
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := costs.Create(f.ctx, cost(p.ID, "130", "115"))
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	cur, err := costs.Current(f.ctx, p.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if cur.ID != second.ID {
		t.Errorf("current: got id %d, want the newest %d", cur.ID, second.ID)
	}

	hist, err := costs.History(f.ctx, p.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 2 || hist[0].ID != second.ID || hist[1].ID != first.ID {
		t.Errorf("history: got %+v, want newest first", hist)
	}
}

func TestCostStore_NoCost(t *testing.T) {
	f := newProductFixture(t)
	costs := postgres.NewCostStore(f.tx)
	p := f.create(t, "TEST-CO-3", "SIN COSTO TEST")

	if _, err := costs.Current(f.ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("current: got %v, want %v", err, store.ErrNotFound)
	}
	hist, err := costs.History(f.ctx, p.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if hist == nil || len(hist) != 0 {
		t.Errorf("history: got %#v, want empty non-nil", hist)
	}
}

func TestCostStore_UnknownProduct(t *testing.T) {
	f := newProductFixture(t)
	costs := postgres.NewCostStore(f.tx)

	_, err := costs.Create(f.ctx, cost(-1, "100", "100"))
	if !errors.Is(err, store.ErrInvalidReference) {
		t.Errorf("got %v, want %v", err, store.ErrInvalidReference)
	}
}

func TestCostStore_Nil(t *testing.T) {
	costs := postgres.NewCostStore(nil)
	if _, err := costs.Create(t.Context(), nil); !errors.Is(err, model.ErrNilCost) {
		t.Errorf("got %v, want %v", err, model.ErrNilCost)
	}
}
