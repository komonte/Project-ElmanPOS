package postgres_test

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

func TestPurchaseStore_CreateAndGet(t *testing.T) {
	f := newProductFixture(t)
	purchases := postgres.NewPurchaseStore(f.tx)
	p1 := f.create(t, "TEST-PU-1", "COMPRA UNO TEST")
	p2 := f.create(t, "TEST-PU-2", "COMPRA DOS TEST")

	got, err := purchases.Create(f.ctx, &model.Purchase{
		SupplierID: f.supplierID, VoucherNumber: "A-0001-00001234", Total: decimal.RequireFromString("1150"),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.ID == 0 || got.SupplierID != f.supplierID || got.VoucherNumber != "A-0001-00001234" ||
		!got.Total.Equal(decimal.RequireFromString("1150")) || got.Status != "RECEIVED" ||
		got.ReceivedBy != nil || got.ReceivedAt == "" {
		t.Errorf("create: got %+v", got)
	}

	// Dos renglones; el segundo con un lote.
	var batchID int64
	err = f.tx.QueryRowContext(f.ctx, `
		INSERT INTO product_batches (product_id, batch_number, original_quantity, remaining_quantity, unit_cost)
		VALUES ($1, 'L-TEST', 2.5, 2.5, 60) RETURNING id;`, p2.ID).Scan(&batchID)
	if err != nil {
		t.Fatalf("fixture batch: %v", err)
	}
	lines := []*model.PurchaseItem{
		{PurchaseID: got.ID, ProductID: p1.ID, Quantity: decimal.RequireFromString("10"), UnitCost: decimal.RequireFromString("100")},
		{PurchaseID: got.ID, ProductID: p2.ID, Quantity: decimal.RequireFromString("2.5"), UnitCost: decimal.RequireFromString("60"), BatchID: &batchID},
	}
	for _, it := range lines {
		saved, err := purchases.CreateItem(f.ctx, it)
		if err != nil {
			t.Fatalf("create item: %v", err)
		}
		if saved.ID == 0 || saved.PurchaseID != got.ID || !saved.Quantity.Equal(it.Quantity) {
			t.Errorf("create item: got %+v", saved)
		}
	}

	read, err := purchases.GetByID(f.ctx, got.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if read.ID != got.ID || len(read.Items) != 2 {
		t.Fatalf("get: got %+v", read)
	}
	if read.Items[0].ProductID != p1.ID || read.Items[0].BatchID != nil {
		t.Errorf("line 1: got %+v", read.Items[0])
	}
	if read.Items[1].ProductID != p2.ID || read.Items[1].BatchID == nil || *read.Items[1].BatchID != batchID {
		t.Errorf("line 2: got %+v", read.Items[1])
	}
}

// Sin número de comprobante se guarda NULL y se lee vacío.
func TestPurchaseStore_NoVoucher(t *testing.T) {
	f := newProductFixture(t)
	purchases := postgres.NewPurchaseStore(f.tx)

	got, err := purchases.Create(f.ctx, &model.Purchase{SupplierID: f.supplierID})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var isNull bool
	if err := f.tx.QueryRowContext(f.ctx,
		`SELECT voucher_number IS NULL FROM purchases WHERE id = $1;`, got.ID).Scan(&isNull); err != nil {
		t.Fatal(err)
	}
	if got.VoucherNumber != "" || !isNull {
		t.Errorf("got voucher %q, NULL in DB = %v", got.VoucherNumber, isNull)
	}

	read, err := purchases.GetByID(f.ctx, got.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if read.Items == nil || len(read.Items) != 0 {
		t.Errorf("items: got %#v, want empty non-nil", read.Items)
	}
}

func TestPurchaseStore_UnknownSupplier(t *testing.T) {
	f := newProductFixture(t)
	purchases := postgres.NewPurchaseStore(f.tx)

	_, err := purchases.Create(f.ctx, &model.Purchase{SupplierID: -1})
	if !errors.Is(err, store.ErrInvalidReference) {
		t.Errorf("unknown supplier: got %v, want %v", err, store.ErrInvalidReference)
	}
}

// Aparte: después de un error, la transacción del test queda abortada.
func TestPurchaseStore_UnknownProductInLine(t *testing.T) {
	f := newProductFixture(t)
	purchases := postgres.NewPurchaseStore(f.tx)

	p, err := purchases.Create(f.ctx, &model.Purchase{SupplierID: f.supplierID})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, err = purchases.CreateItem(f.ctx, &model.PurchaseItem{PurchaseID: p.ID, ProductID: -1,
		Quantity: decimal.NewFromInt(1), UnitCost: decimal.NewFromInt(1)})
	if !errors.Is(err, store.ErrInvalidReference) {
		t.Errorf("unknown product: got %v, want %v", err, store.ErrInvalidReference)
	}
}

func TestPurchaseStore_NotFound(t *testing.T) {
	f := newProductFixture(t)
	if _, err := postgres.NewPurchaseStore(f.tx).GetByID(f.ctx, -1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("got %v, want %v", err, store.ErrNotFound)
	}
}

func TestPurchaseStore_Nil(t *testing.T) {
	purchases := postgres.NewPurchaseStore(nil)
	if _, err := purchases.Create(t.Context(), nil); !errors.Is(err, model.ErrNilPurchase) {
		t.Errorf("create: got %v", err)
	}
	if _, err := purchases.CreateItem(t.Context(), nil); !errors.Is(err, model.ErrNilPurchaseItem) {
		t.Errorf("create item: got %v", err)
	}
}
