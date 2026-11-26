package postgres_test

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

func TestStockAdjustmentStore_CreateAndRecent(t *testing.T) {
	f := newProductFixture(t)
	adjustments := postgres.NewStockAdjustmentStore(f.tx)
	storeID := f.insertID(t, `INSERT INTO stores (name) VALUES ('Local test salidas') RETURNING id;`)
	other := f.insertID(t, `INSERT INTO stores (name) VALUES ('Otro local test salidas') RETURNING id;`)
	p := f.create(t, "TEST-SA-1", "YERBA SALIDA TEST")

	add := func(store int64, reason, qty, notes string) *model.StockAdjustment {
		t.Helper()
		a, err := adjustments.Create(f.ctx, &model.StockAdjustment{StoreID: store, ProductID: p.ID, Reason: reason,
			Quantity: decimal.RequireFromString(qty), Notes: notes})
		if err != nil {
			t.Fatalf("create %s: %v", reason, err)
		}
		return a
	}
	first := add(storeID, model.ReasonOwnConsumption, "1", "")
	if first.ID == 0 || first.CreatedAt == "" || first.CreatedBy != nil {
		t.Errorf("create: got %+v", first)
	}
	var notesNull bool
	if err := f.tx.QueryRowContext(f.ctx, `SELECT notes IS NULL FROM stock_adjustments WHERE id = $1;`,
		first.ID).Scan(&notesNull); err != nil || !notesNull {
		t.Errorf("empty notes must be NULL: %v, %v", notesNull, err)
	}
	add(storeID, model.ReasonDamage, "2", "se cayó")
	last := add(storeID, model.ReasonOther, "0.5", "muestra")
	add(other, model.ReasonExpiration, "9", "")

	got, err := adjustments.Recent(f.ctx, storeID, 2)
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	if len(got) != 2 || got[0].ID != last.ID || got[0].Notes != "muestra" || got[0].ProductName != "YERBA SALIDA TEST" ||
		got[1].Reason != model.ReasonDamage {
		t.Errorf("recent: got %+v", got)
	}
	if all, _ := adjustments.Recent(f.ctx, storeID, 50); len(all) != 3 {
		t.Errorf("recent of this store: got %d, want 3", len(all))
	}
}

func TestStockAdjustmentStore_Rules(t *testing.T) {
	f := newProductFixture(t)
	adjustments := postgres.NewStockAdjustmentStore(f.tx)
	storeID := f.insertID(t, `INSERT INTO stores (name) VALUES ('Local test reglas salidas') RETURNING id;`)
	p := f.create(t, "TEST-SA-2", "FIDEOS SALIDA TEST")

	err := f.failing(t, func() error {
		_, err := adjustments.Create(f.ctx, &model.StockAdjustment{StoreID: storeID, ProductID: -1,
			Reason: model.ReasonDamage, Quantity: decimal.NewFromInt(1)})
		return err
	})
	if !errors.Is(err, store.ErrInvalidReference) {
		t.Errorf("unknown product: got %v, want %v", err, store.ErrInvalidReference)
	}

	// La DB también rechaza "otro" sin nota y el robo (Validate lo revisa antes).
	for _, a := range []*model.StockAdjustment{
		{StoreID: storeID, ProductID: p.ID, Reason: model.ReasonOther, Quantity: decimal.NewFromInt(1)},
		{StoreID: storeID, ProductID: p.ID, Reason: "THEFT", Quantity: decimal.NewFromInt(1)},
	} {
		if err := f.failing(t, func() error { _, err := adjustments.Create(f.ctx, a); return err }); err == nil {
			t.Errorf("reason %s without notes: got no error", a.Reason)
		}
	}

	// Solo se agregan filas.
	a, err := adjustments.Create(f.ctx, &model.StockAdjustment{StoreID: storeID, ProductID: p.ID,
		Reason: model.ReasonDamage, Quantity: decimal.NewFromInt(1)})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`UPDATE stock_adjustments SET notes = 'x' WHERE id = $1;`,
		`DELETE FROM stock_adjustments WHERE id = $1;`} {
		if err := f.failing(t, func() error { _, err := f.tx.ExecContext(f.ctx, q, a.ID); return err }); err == nil {
			t.Errorf("%s: got no error", q)
		}
	}
}

// Un movimiento de una salida conocida solo puede restar.
func TestStockMovementStore_AdjustmentExit(t *testing.T) {
	f := newProductFixture(t)
	p := f.create(t, "TEST-SA-3", "ARROZ SALIDA TEST")
	m := f.purchaseMovement(t, p.ID)
	a, err := postgres.NewStockAdjustmentStore(f.tx).Create(f.ctx, &model.StockAdjustment{StoreID: m.StoreID,
		ProductID: p.ID, Reason: model.ReasonDamage, Quantity: decimal.NewFromInt(2)})
	if err != nil {
		t.Fatal(err)
	}
	m.MovementType, m.Reason, m.PurchaseID, m.AdjustmentID = model.MovementAdjustment, model.ReasonDamage, nil, &a.ID
	m.Quantity = decimal.NewFromInt(-2)

	movements := postgres.NewStockMovementStore(f.tx)
	got, err := movements.Create(f.ctx, m)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.AdjustmentID == nil || *got.AdjustmentID != a.ID || got.Reason != "DAMAGE" || got.PurchaseID != nil {
		t.Errorf("create: got %+v", got)
	}
	m.Quantity = decimal.NewFromInt(2)
	if err := f.failing(t, func() error { _, err := movements.Create(f.ctx, m); return err }); err == nil {
		t.Error("positive known exit: got no error")
	}
}
