package postgres_test

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

func TestBatchStore_CreateAndAdd(t *testing.T) {
	f := newProductFixture(t)
	batches := postgres.NewBatchStore(f.tx)
	p := f.create(t, "TEST-BA-1", "LOTE TEST")

	b, err := batches.Create(f.ctx, &model.ProductBatch{ProductID: p.ID, BatchNumber: "L-2475",
		ExpiryDate: "2027-03-31", OriginalQuantity: decimal.RequireFromString("2.5"),
		UnitCost: decimal.RequireFromString("60")})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if b.ID == 0 || b.ProductID != p.ID || b.BatchNumber != "L-2475" || b.ExpiryDate != "2027-03-31" ||
		!b.OriginalQuantity.Equal(decimal.RequireFromString("2.5")) ||
		!b.RemainingQuantity.Equal(decimal.RequireFromString("2.5")) ||
		!b.UnitCost.Equal(decimal.RequireFromString("60")) || b.IsConsumed {
		t.Errorf("create: got %+v", b)
	}

	added, err := batches.AddQuantity(f.ctx, b.ID, decimal.RequireFromString("1.25"))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !added.OriginalQuantity.Equal(decimal.RequireFromString("3.75")) ||
		!added.RemainingQuantity.Equal(decimal.RequireFromString("3.75")) {
		t.Errorf("add: got original %s remaining %s, want 3.75 both",
			added.OriginalQuantity, added.RemainingQuantity)
	}
}

// Un lote sin vencimiento (no perecedero) guarda NULL y se lee vacío.
func TestBatchStore_NoExpiry(t *testing.T) {
	f := newProductFixture(t)
	batches := postgres.NewBatchStore(f.tx)
	p := f.create(t, "TEST-BA-2", "LOTE SIN VENCIMIENTO TEST")

	b, err := batches.Create(f.ctx, &model.ProductBatch{ProductID: p.ID, BatchNumber: "L-1",
		OriginalQuantity: decimal.NewFromInt(1), UnitCost: decimal.NewFromInt(1)})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var isNull bool
	if err := f.tx.QueryRowContext(f.ctx,
		`SELECT expiry_date IS NULL FROM product_batches WHERE id = $1;`, b.ID).Scan(&isNull); err != nil {
		t.Fatal(err)
	}
	if b.ExpiryDate != "" || !isNull {
		t.Errorf("got expiry %q, NULL in DB = %v", b.ExpiryDate, isNull)
	}
}

func TestBatchStore_UnknownProduct(t *testing.T) {
	f := newProductFixture(t)
	_, err := postgres.NewBatchStore(f.tx).Create(f.ctx, &model.ProductBatch{ProductID: -1, BatchNumber: "L-1",
		OriginalQuantity: decimal.NewFromInt(1), UnitCost: decimal.NewFromInt(1)})
	if !errors.Is(err, store.ErrInvalidReference) {
		t.Errorf("got %v, want %v", err, store.ErrInvalidReference)
	}
}

func TestBatchStore_AddToMissingBatch(t *testing.T) {
	f := newProductFixture(t)
	_, err := postgres.NewBatchStore(f.tx).AddQuantity(f.ctx, -1, decimal.NewFromInt(1))
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("got %v, want %v", err, store.ErrNotFound)
	}
}
