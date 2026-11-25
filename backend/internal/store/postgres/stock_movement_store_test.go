package postgres_test

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// purchaseMovement arma una compra y un local en la transacción del test y
// devuelve un movimiento de compra válido para el producto.
func (f *productFixture) purchaseMovement(t *testing.T, productID int64) *model.StockMovement {
	t.Helper()
	pu, err := postgres.NewPurchaseStore(f.tx).Create(f.ctx, &model.Purchase{SupplierID: f.supplierID})
	if err != nil {
		t.Fatalf("fixture purchase: %v", err)
	}
	var storeID int64
	if err := f.tx.QueryRowContext(f.ctx,
		`INSERT INTO stores (name) VALUES ('Local test movement') RETURNING id;`).Scan(&storeID); err != nil {
		t.Fatalf("fixture store: %v", err)
	}
	return &model.StockMovement{ProductID: productID, StoreID: storeID, MovementType: model.MovementPurchase,
		Quantity: decimal.RequireFromString("2.5"), UnitCost: decimal.RequireFromString("60"), PurchaseID: &pu.ID}
}

func TestStockMovementStore_Create(t *testing.T) {
	f := newProductFixture(t)
	p := f.create(t, "TEST-SM-1", "MOVIMIENTO TEST")
	m := f.purchaseMovement(t, p.ID)

	got, err := postgres.NewStockMovementStore(f.tx).Create(f.ctx, m)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.ID == 0 || got.ProductID != p.ID || got.StoreID != m.StoreID || got.MovementType != "PURCHASE" ||
		!got.Quantity.Equal(decimal.RequireFromString("2.5")) || !got.UnitCost.Equal(decimal.RequireFromString("60")) ||
		got.PurchaseID == nil || *got.PurchaseID != *m.PurchaseID || got.BatchID != nil ||
		got.PerformedBy != nil || got.CreatedAt == "" {
		t.Errorf("create: got %+v", got)
	}
}

// Una compra resta stock: la DB lo rechaza (CHECK del signo por tipo).
func TestStockMovementStore_PurchaseMustBePositive(t *testing.T) {
	f := newProductFixture(t)
	p := f.create(t, "TEST-SM-2", "MOVIMIENTO NEGATIVO TEST")
	m := f.purchaseMovement(t, p.ID)
	m.Quantity = decimal.RequireFromString("-1")

	if _, err := postgres.NewStockMovementStore(f.tx).Create(f.ctx, m); err == nil {
		t.Error("negative purchase movement: got no error")
	}
}

func TestStockMovementStore_UnknownProduct(t *testing.T) {
	f := newProductFixture(t)
	m := f.purchaseMovement(t, -1)
	_, err := postgres.NewStockMovementStore(f.tx).Create(f.ctx, m)
	if !errors.Is(err, store.ErrInvalidReference) {
		t.Errorf("got %v, want %v", err, store.ErrInvalidReference)
	}
}

func TestStockMovementStore_Nil(t *testing.T) {
	if _, err := postgres.NewStockMovementStore(nil).Create(t.Context(), nil); !errors.Is(err, model.ErrNilStockMovement) {
		t.Errorf("got %v", err)
	}
}
