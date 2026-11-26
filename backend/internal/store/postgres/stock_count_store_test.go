package postgres_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// countFixture: un local propio y un conteo en borrador, en la transacción
// del test.
func (f *productFixture) countFixture(t *testing.T) (*postgres.StockCountStore, *model.StockCount) {
	t.Helper()
	storeID := f.insertID(t, `INSERT INTO stores (name) VALUES ('Local test conteo') RETURNING id;`)
	counts := postgres.NewStockCountStore(f.tx)
	c, err := counts.CreateDraft(f.ctx, storeID)
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}
	return counts, c
}

// failing corre fn, que tiene que fallar en la DB, dentro de un savepoint:
// un error de Postgres aborta la transacción del test y lo que sigue
// fallaría con 25P02. Devuelve el error de fn.
func (f *productFixture) failing(t *testing.T, fn func() error) error {
	t.Helper()
	if _, err := f.tx.ExecContext(f.ctx, `SAVEPOINT expected_failure;`); err != nil {
		t.Fatal(err)
	}
	err := fn()
	if _, rbErr := f.tx.ExecContext(f.ctx, `ROLLBACK TO SAVEPOINT expected_failure;`); rbErr != nil {
		t.Fatal(rbErr)
	}
	return err
}

func TestStockCountStore_Draft(t *testing.T) {
	f := newProductFixture(t)
	counts, c := f.countFixture(t)
	if c.ID == 0 || c.Status != model.CountDraft || c.CreatedBy != nil || c.ConfirmedAt != nil || c.CreatedAt == "" {
		t.Errorf("create: got %+v", c)
	}

	got, err := counts.Draft(f.ctx, c.StoreID)
	if err != nil || got.ID != c.ID {
		t.Errorf("draft: got %+v, %v", got, err)
	}
	err = f.failing(t, func() error { _, err := counts.CreateDraft(f.ctx, c.StoreID); return err })
	if !errors.Is(err, store.ErrCountDraftExists) {
		t.Errorf("second draft: got %v, want %v", err, store.ErrCountDraftExists)
	}
	if !errors.Is(store.ErrCountDraftExists, store.ErrConflict) {
		t.Error("ErrCountDraftExists must be a conflict")
	}

	other := f.insertID(t, `INSERT INTO stores (name) VALUES ('Otro local test conteo') RETURNING id;`)
	if _, err := counts.Draft(f.ctx, other); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("draft of a store without one: got %v, want %v", err, store.ErrNotFound)
	}
	err = f.failing(t, func() error { _, err := counts.CreateDraft(f.ctx, -1); return err })
	if !errors.Is(err, store.ErrInvalidReference) {
		t.Errorf("unknown store: got %v, want %v", err, store.ErrInvalidReference)
	}
}

func TestStockCountStore_ItemsAndEntries(t *testing.T) {
	f := newProductFixture(t)
	counts, c := f.countFixture(t)
	yerba := f.create(t, "TEST-SC-1", "YERBA CONTEO TEST")
	azucar := f.create(t, "TEST-SC-2", "AZUCAR CONTEO TEST")

	it, err := counts.CreateItem(f.ctx, c.ID, yerba.ID, decimal.NewFromInt(10))
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if it.ID == 0 || it.CountID != c.ID || it.ProductID != yerba.ID || !it.SystemQuantity.Equal(decimal.NewFromInt(10)) ||
		!it.CountedQuantity.IsZero() || it.UnitCost != nil || it.ExpiryDate != "" {
		t.Errorf("create item: got %+v", it)
	}
	err = f.failing(t, func() error { _, err := counts.CreateItem(f.ctx, c.ID, yerba.ID, decimal.Zero); return err })
	if !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("same product twice: got %v, want %v", err, store.ErrDuplicate)
	}
	err = f.failing(t, func() error { _, err := counts.CreateItem(f.ctx, c.ID, -1, decimal.Zero); return err })
	if !errors.Is(err, store.ErrInvalidReference) {
		t.Errorf("unknown product: got %v, want %v", err, store.ErrInvalidReference)
	}

	e1, err := counts.AddEntry(f.ctx, it.ID, "Góndola 3", decimal.NewFromInt(3))
	if err != nil {
		t.Fatalf("entry 1: %v", err)
	}
	if e1.ID == 0 || e1.ItemID != it.ID || e1.Sector != "Góndola 3" || !e1.Quantity.Equal(decimal.NewFromInt(3)) {
		t.Errorf("entry 1: got %+v", e1)
	}
	if _, err := counts.AddEntry(f.ctx, it.ID, "Depósito", decimal.RequireFromString("4.5")); err != nil {
		t.Fatalf("entry 2: %v", err)
	}
	err = f.failing(t, func() error { _, err := counts.AddEntry(f.ctx, -1, "Depósito", decimal.NewFromInt(1)); return err })
	if !errors.Is(err, store.ErrInvalidReference) {
		t.Errorf("entry of unknown item: got %v, want %v", err, store.ErrInvalidReference)
	}
	az, err := counts.CreateItem(f.ctx, c.ID, azucar.ID, decimal.NewFromInt(2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := counts.AddEntry(f.ctx, az.ID, "Depósito", decimal.Zero); err != nil {
		t.Fatalf("zero entry: %v", err)
	}

	got, err := counts.Item(f.ctx, c.ID, yerba.ID)
	if err != nil || !got.CountedQuantity.Equal(decimal.RequireFromString("7.5")) {
		t.Errorf("item: got %+v, %v; want counted 7.5", got, err)
	}
	if _, err := counts.Item(f.ctx, c.ID, -1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("item not counted: got %v, want %v", err, store.ErrNotFound)
	}

	// Por nombre: azúcar antes que yerba; las cargas en orden.
	full, err := counts.GetByID(f.ctx, c.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(full.Items) != 2 || full.Items[0].ProductName != "AZUCAR CONTEO TEST" || full.Items[1].ProductName != "YERBA CONTEO TEST" {
		t.Fatalf("get items: got %+v", full.Items)
	}
	y := full.Items[1]
	if len(y.Entries) != 2 || y.Entries[0].Sector != "Góndola 3" || y.Entries[1].Sector != "Depósito" ||
		y.Entries[0].ProductID != yerba.ID || !y.CountedQuantity.Equal(decimal.RequireFromString("7.5")) {
		t.Errorf("get entries: got %+v", y.Entries)
	}
	if full.Items[0].ControlsBatches || full.Items[0].AvgCost != nil {
		t.Errorf("azucar: got batches %v avg %v, want false and nil", full.Items[0].ControlsBatches, full.Items[0].AvgCost)
	}

	// El costo que trae es el vigente (el último) y el lote, el del producto.
	costs := postgres.NewCostStore(f.tx)
	for _, c := range []string{"100", "120"} {
		if _, err := costs.Create(f.ctx, &model.Cost{ProductID: yerba.ID, UnitCost: decimal.RequireFromString(c),
			AvgCost: decimal.RequireFromString(c)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.tx.ExecContext(f.ctx, `UPDATE products SET controls_batches = TRUE WHERE id = $1;`, yerba.ID); err != nil {
		t.Fatal(err)
	}
	full, _ = counts.GetByID(f.ctx, c.ID)
	if y := full.Items[1]; !y.ControlsBatches || y.AvgCost == nil || !y.AvgCost.Equal(decimal.RequireFromString("120")) {
		t.Errorf("yerba: got batches %v avg %v, want true and 120", y.ControlsBatches, y.AvgCost)
	}

	if _, err := counts.GetByID(f.ctx, -1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get missing: got %v, want %v", err, store.ErrNotFound)
	}

	sectors, err := counts.Sectors(f.ctx)
	if err != nil || !slices.Contains(sectors, "Góndola 3") || !slices.Contains(sectors, "Depósito") ||
		!slices.IsSorted(sectors) || len(slices.Compact(slices.Clone(sectors))) != len(sectors) {
		t.Errorf("sectors: got %v, %v", sectors, err)
	}
}

func TestStockCountStore_SetItemDetails(t *testing.T) {
	f := newProductFixture(t)
	counts, c := f.countFixture(t)
	p := f.create(t, "TEST-SC-3", "QUESO CONTEO TEST")
	it, err := counts.CreateItem(f.ctx, c.ID, p.ID, decimal.Zero)
	if err != nil {
		t.Fatal(err)
	}

	cost := decimal.RequireFromString("850.50")
	if err := counts.SetItemDetails(f.ctx, c.ID, it.ID, &cost, "2027-03-31"); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, _ := counts.Item(f.ctx, c.ID, p.ID)
	if got.UnitCost == nil || !got.UnitCost.Equal(cost) || got.ExpiryDate != "2027-03-31" {
		t.Errorf("set: got cost %v expiry %q", got.UnitCost, got.ExpiryDate)
	}

	if err := counts.SetItemDetails(f.ctx, c.ID, it.ID, nil, ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	got, _ = counts.Item(f.ctx, c.ID, p.ID)
	if got.UnitCost != nil || got.ExpiryDate != "" {
		t.Errorf("clear: got cost %v expiry %q", got.UnitCost, got.ExpiryDate)
	}

	if err := counts.SetItemDetails(f.ctx, -1, it.ID, nil, ""); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("other count: got %v, want %v", err, store.ErrNotFound)
	}
}

func TestStockCountStore_DeleteEntry(t *testing.T) {
	f := newProductFixture(t)
	counts, c := f.countFixture(t)
	p := f.create(t, "TEST-SC-4", "FIDEOS CONTEO TEST")
	it, err := counts.CreateItem(f.ctx, c.ID, p.ID, decimal.NewFromInt(5))
	if err != nil {
		t.Fatal(err)
	}
	e1, _ := counts.AddEntry(f.ctx, it.ID, "Góndola 1", decimal.NewFromInt(2))
	e2, _ := counts.AddEntry(f.ctx, it.ID, "Depósito", decimal.NewFromInt(6))

	if _, err := counts.DeleteEntry(f.ctx, -1, e1.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("other count: got %v, want %v", err, store.ErrNotFound)
	}

	deleted, err := counts.DeleteEntry(f.ctx, c.ID, e1.ID)
	if err != nil || deleted {
		t.Fatalf("delete first: deleted %v, %v", deleted, err)
	}
	got, err := counts.Item(f.ctx, c.ID, p.ID)
	if err != nil || !got.CountedQuantity.Equal(decimal.NewFromInt(6)) {
		t.Errorf("after first: got %+v, %v; want counted 6", got, err)
	}
	if _, err := counts.DeleteEntry(f.ctx, c.ID, e1.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete twice: got %v, want %v", err, store.ErrNotFound)
	}

	// La última carga se lleva el renglón: el producto vuelve a "no contado".
	deleted, err = counts.DeleteEntry(f.ctx, c.ID, e2.ID)
	if err != nil || !deleted {
		t.Fatalf("delete last: deleted %v, %v", deleted, err)
	}
	if _, err := counts.Item(f.ctx, c.ID, p.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("item after last entry: got %v, want %v", err, store.ErrNotFound)
	}
}

func TestStockCountStore_ConfirmAndDiscard(t *testing.T) {
	f := newProductFixture(t)
	counts, c := f.countFixture(t)
	p := f.create(t, "TEST-SC-5", "ARROZ CONTEO TEST")

	if got, err := counts.LockDraft(f.ctx, c.ID); err != nil || got.ID != c.ID {
		t.Fatalf("lock draft: got %+v, %v", got, err)
	}
	if _, err := counts.LockDraft(f.ctx, -1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("lock missing: got %v, want %v", err, store.ErrNotFound)
	}

	confirmed, err := counts.Confirm(f.ctx, c.ID)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if confirmed.Status != model.CountConfirmed || confirmed.ConfirmedAt == nil {
		t.Errorf("confirm: got %+v", confirmed)
	}
	if _, err := counts.Confirm(f.ctx, c.ID); !errors.Is(err, store.ErrCountNotDraft) {
		t.Errorf("confirm twice: got %v, want %v", err, store.ErrCountNotDraft)
	}
	if _, err := counts.Confirm(f.ctx, -1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("confirm missing: got %v, want %v", err, store.ErrNotFound)
	}
	if _, err := counts.LockDraft(f.ctx, c.ID); !errors.Is(err, store.ErrCountNotDraft) {
		t.Errorf("lock confirmed: got %v, want %v", err, store.ErrCountNotDraft)
	}
	if err := counts.DeleteDraft(f.ctx, c.ID); !errors.Is(err, store.ErrCountNotDraft) {
		t.Errorf("discard confirmed: got %v, want %v", err, store.ErrCountNotDraft)
	}

	// Con el anterior confirmado se puede abrir otro; descartarlo borra todo.
	draft, err := counts.CreateDraft(f.ctx, c.StoreID)
	if err != nil {
		t.Fatalf("new draft after confirm: %v", err)
	}
	it, err := counts.CreateItem(f.ctx, draft.ID, p.ID, decimal.Zero)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := counts.AddEntry(f.ctx, it.ID, "Depósito", decimal.NewFromInt(1)); err != nil {
		t.Fatal(err)
	}
	if err := counts.DeleteDraft(f.ctx, draft.ID); err != nil {
		t.Fatalf("discard: %v", err)
	}
	var left int
	if err := f.tx.QueryRowContext(f.ctx,
		`SELECT count(*) FROM stock_count_items WHERE count_id = $1;`, draft.ID).Scan(&left); err != nil || left != 0 {
		t.Errorf("items after discard: %d, %v", left, err)
	}
	if err := counts.DeleteDraft(f.ctx, draft.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("discard twice: got %v, want %v", err, store.ErrNotFound)
	}
}
