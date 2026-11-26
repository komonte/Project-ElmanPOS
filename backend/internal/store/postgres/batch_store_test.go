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

// TakeFIFO saca primero del lote que vence antes; los sin vencimiento van al
// final. Lo que los lotes no alcanzan a cubrir vuelve como left.
func TestBatchStore_TakeFIFO(t *testing.T) {
	f := newProductFixture(t)
	batches := postgres.NewBatchStore(f.tx)
	p := f.create(t, "TEST-BA-3", "LOTE FIFO TEST")
	other := f.create(t, "TEST-BA-4", "OTRO LOTE FIFO TEST")

	newBatch := func(productID int64, number, expiry, qty, cost string) int64 {
		t.Helper()
		b, err := batches.Create(f.ctx, &model.ProductBatch{ProductID: productID, BatchNumber: number,
			ExpiryDate: expiry, OriginalQuantity: decimal.RequireFromString(qty),
			UnitCost: decimal.RequireFromString(cost)})
		if err != nil {
			t.Fatalf("create %s: %v", number, err)
		}
		return b.ID
	}
	noExpiry := newBatch(p.ID, "SIN-VTO", "", "4", "70")
	later := newBatch(p.ID, "L-ENERO", "2027-01-31", "2", "60")
	sooner := newBatch(p.ID, "L-DICIEMBRE", "2026-12-31", "3", "50")
	newBatch(other.ID, "OTRO", "2026-01-01", "9", "10") // otro producto: no se toca

	type want struct {
		id  int64
		qty string
	}
	check := func(qty string, wantTakes []want, wantLeft string) {
		t.Helper()
		takes, left, err := batches.TakeFIFO(f.ctx, p.ID, decimal.RequireFromString(qty))
		if err != nil {
			t.Fatalf("take %s: %v", qty, err)
		}
		if len(takes) != len(wantTakes) || !left.Equal(decimal.RequireFromString(wantLeft)) {
			t.Fatalf("take %s: got %+v left %s, want %v left %s", qty, takes, left, wantTakes, wantLeft)
		}
		for i, w := range wantTakes {
			if takes[i].BatchID != w.id || !takes[i].Quantity.Equal(decimal.RequireFromString(w.qty)) {
				t.Errorf("take %s, #%d: got %+v, want %+v", qty, i, takes[i], w)
			}
		}
	}
	remaining := func(id int64) (string, bool) {
		t.Helper()
		var qty decimal.Decimal
		var consumed bool
		if err := f.tx.QueryRowContext(f.ctx,
			`SELECT remaining_quantity, is_consumed FROM product_batches WHERE id = $1;`, id).Scan(&qty, &consumed); err != nil {
			t.Fatal(err)
		}
		return qty.String(), consumed
	}

	check("4", []want{{sooner, "3"}, {later, "1"}}, "0")
	if qty, consumed := remaining(sooner); qty != "0" || !consumed {
		t.Errorf("sooner: remaining %s consumed %v, want 0 true", qty, consumed)
	}
	if qty, consumed := remaining(later); qty != "1" || consumed {
		t.Errorf("later: remaining %s consumed %v, want 1 false", qty, consumed)
	}

	// El consumido no vuelve a aparecer; no alcanza: sobran 5 sin lote.
	check("10", []want{{later, "1"}, {noExpiry, "4"}}, "5")

	// Sin lotes con mercadería: todo vuelve como left.
	check("1.5", nil, "1.5")

	// El costo de cada salida es el de su lote.
	newBatch(p.ID, "L-COSTO", "", "1", "82.50")
	takes, _, err := batches.TakeFIFO(f.ctx, p.ID, decimal.NewFromInt(1))
	if err != nil || len(takes) != 1 || !takes[0].UnitCost.Equal(decimal.RequireFromString("82.50")) {
		t.Errorf("cost: got %+v, %v", takes, err)
	}
}
