package postgres_test

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

func price(productID int64, amount string) *model.Price {
	return &model.Price{ProductID: productID, UnitPrice: decimal.RequireFromString(amount)}
}

func TestPriceStore_CreateAndCurrent(t *testing.T) {
	f := newProductFixture(t)
	prices := postgres.NewPriceStore(f.tx)
	p := f.create(t, "TEST-PR-1", "PRECIO TEST")

	got, err := prices.Create(f.ctx, price(p.ID, "1500.50"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.ID == 0 || got.ProductID != p.ID || !got.UnitPrice.Equal(decimal.RequireFromString("1500.50")) ||
		got.ValidFrom == "" || got.CreatedAt == "" {
		t.Errorf("create: got %+v", got)
	}

	cur, err := prices.Current(f.ctx, p.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if cur.ID != got.ID {
		t.Errorf("current: got id %d, want %d", cur.ID, got.ID)
	}
}

// Dos precios en la misma transacción (la del test) no chocan: valid_from es
// la hora real (clock_timestamp), no la del inicio de la transacción.
func TestPriceStore_NewPriceReplacesCurrent(t *testing.T) {
	f := newProductFixture(t)
	prices := postgres.NewPriceStore(f.tx)
	p := f.create(t, "TEST-PR-2", "CAMBIO PRECIO TEST")

	first, err := prices.Create(f.ctx, price(p.ID, "100"))
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := prices.Create(f.ctx, price(p.ID, "120"))
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	cur, err := prices.Current(f.ctx, p.ID)
	if err != nil {
		t.Fatalf("current: %v", err)
	}
	if cur.ID != second.ID {
		t.Errorf("current: got id %d, want the newest %d", cur.ID, second.ID)
	}

	hist, err := prices.History(f.ctx, p.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 2 || hist[0].ID != second.ID || hist[1].ID != first.ID {
		t.Errorf("history: got %+v, want newest first", hist)
	}
}

func TestPriceStore_NoPrice(t *testing.T) {
	f := newProductFixture(t)
	prices := postgres.NewPriceStore(f.tx)
	p := f.create(t, "TEST-PR-3", "SIN PRECIO TEST")

	if _, err := prices.Current(f.ctx, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("current: got %v, want %v", err, store.ErrNotFound)
	}
	hist, err := prices.History(f.ctx, p.ID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if hist == nil || len(hist) != 0 {
		t.Errorf("history: got %#v, want empty non-nil", hist)
	}
}

func TestPriceStore_UnknownProduct(t *testing.T) {
	f := newProductFixture(t)
	prices := postgres.NewPriceStore(f.tx)

	_, err := prices.Create(f.ctx, price(-1, "100"))
	if !errors.Is(err, store.ErrInvalidReference) {
		t.Errorf("got %v, want %v", err, store.ErrInvalidReference)
	}
}
