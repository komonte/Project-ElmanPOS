package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// addBalance crea un local y le pone saldo al producto. Cada llamada usa un
// local nuevo: la PK de stock_balances es (product_id, store_id).
func (f *productFixture) addBalance(t *testing.T, productID int64, qty string) {
	t.Helper()
	var storeID int64
	err := f.tx.QueryRowContext(f.ctx,
		`INSERT INTO stores (name) VALUES ('Local test stock') RETURNING id;`).Scan(&storeID)
	if err != nil {
		t.Fatalf("fixture store: %v", err)
	}
	_, err = f.tx.ExecContext(f.ctx,
		`INSERT INTO stock_balances (product_id, store_id, quantity) VALUES ($1, $2, $3);`,
		productID, storeID, qty)
	if err != nil {
		t.Fatalf("fixture balance: %v", err)
	}
}

func TestStockBalanceStore_OnHand(t *testing.T) {
	f := newProductFixture(t)
	balances := postgres.NewStockBalanceStore(f.tx)

	none := f.create(t, "TEST-SB-1", "SIN STOCK TEST")
	one := f.create(t, "TEST-SB-2", "UN LOCAL TEST")
	two := f.create(t, "TEST-SB-3", "DOS LOCALES TEST")
	f.addBalance(t, one.ID, "12")
	f.addBalance(t, two.ID, "2.5")
	f.addBalance(t, two.ID, "1.25")

	tests := []struct {
		name      string
		productID int64
		want      string
	}{
		{name: "no balance row", productID: none.ID, want: "0"},
		{name: "one store", productID: one.ID, want: "12"},
		{name: "sums all stores (kg)", productID: two.ID, want: "3.75"},
		{name: "unknown product", productID: -1, want: "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := balances.OnHand(f.ctx, tt.productID)
			if err != nil {
				t.Fatalf("on hand: %v", err)
			}
			if !got.Equal(decimal.RequireFromString(tt.want)) {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}

func TestStockBalanceStore_Add(t *testing.T) {
	f := newProductFixture(t)
	balances := postgres.NewStockBalanceStore(f.tx)
	p := f.create(t, "TEST-SB-4", "SUMAR STOCK TEST")
	var storeID int64
	if err := f.tx.QueryRowContext(f.ctx,
		`INSERT INTO stores (name) VALUES ('Local test add') RETURNING id;`).Scan(&storeID); err != nil {
		t.Fatal(err)
	}

	// Primera vez: crea la fila. Segunda: suma.
	for _, step := range []struct{ add, want string }{{"10", "10"}, {"2.5", "12.5"}} {
		got, err := balances.Add(f.ctx, p.ID, storeID, decimal.RequireFromString(step.add))
		if err != nil {
			t.Fatalf("add %s: %v", step.add, err)
		}
		if !got.Equal(decimal.RequireFromString(step.want)) {
			t.Errorf("add %s: got %s, want %s", step.add, got, step.want)
		}
	}

	locked, err := balances.LockOnHand(f.ctx, p.ID)
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
	if !locked.Equal(decimal.RequireFromString("12.5")) {
		t.Errorf("lock: got %s, want 12.5", locked)
	}
}

func TestStockBalanceStore_LockOnHandWithoutBalance(t *testing.T) {
	f := newProductFixture(t)
	p := f.create(t, "TEST-SB-5", "SIN SALDO LOCK TEST")
	got, err := postgres.NewStockBalanceStore(f.tx).LockOnHand(f.ctx, p.ID)
	if err != nil || !got.IsZero() {
		t.Errorf("got %s, %v, want 0", got, err)
	}
}

func TestStockBalanceStore_LockOnHandUnknownProduct(t *testing.T) {
	f := newProductFixture(t)
	_, err := postgres.NewStockBalanceStore(f.tx).LockOnHand(f.ctx, -1)
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("got %v, want %v", err, store.ErrNotFound)
	}
}

func TestStockBalanceStore_AddUnknownStore(t *testing.T) {
	f := newProductFixture(t)
	p := f.create(t, "TEST-SB-6", "LOCAL INEXISTENTE TEST")
	_, err := postgres.NewStockBalanceStore(f.tx).Add(f.ctx, p.ID, -1, decimal.NewFromInt(1))
	if !errors.Is(err, store.ErrInvalidReference) {
		t.Errorf("got %v, want %v", err, store.ErrInvalidReference)
	}
}

// Dos transacciones de verdad: la segunda espera a que la primera termine y
// lee el stock que dejó. Incluso sin saldo previo (bloquea el producto).
// Guarda de verdad, así que borra lo suyo al terminar (sin movimientos: esos
// no se pueden borrar).
func TestStockBalanceStore_LockOnHandWaits(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	scan := func(dest *int64, q string, args ...any) {
		t.Helper()
		if err := db.QueryRowContext(ctx, q, args...).Scan(dest); err != nil {
			t.Fatalf("setup %q: %v", q, err)
		}
	}
	var taxID, categoryID, productID, storeID int64
	scan(&taxID, `INSERT INTO taxes (name, rate) VALUES ('IVA test lock', 21) RETURNING id;`)
	scan(&categoryID, `INSERT INTO categories (name, tax_id) VALUES ('Categoria test lock', $1) RETURNING id;`, taxID)
	scan(&productID, `INSERT INTO products (name, category_id) VALUES ('PRODUCTO TEST LOCK', $1) RETURNING id;`, categoryID)
	// Inactivo: así no cuenta como "otro local activo" si algo corre en paralelo.
	scan(&storeID, `INSERT INTO stores (name, is_active) VALUES ('Local test lock', FALSE) RETURNING id;`)
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM stock_balances WHERE product_id = $1;`,
			`DELETE FROM products WHERE id = $1;`,
		} {
			if _, err := db.ExecContext(ctx, q, productID); err != nil {
				t.Errorf("cleanup %q: %v", q, err)
			}
		}
		db.ExecContext(ctx, `DELETE FROM stores WHERE id = $1;`, storeID)
		db.ExecContext(ctx, `DELETE FROM categories WHERE id = $1;`, categoryID)
		db.ExecContext(ctx, `DELETE FROM taxes WHERE id = $1;`, taxID)
	})

	first, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback()
	if qty, err := postgres.NewStockBalanceStore(first).LockOnHand(ctx, productID); err != nil || !qty.IsZero() {
		t.Fatalf("first lock: got %s, %v", qty, err)
	}

	// La segunda transacción intenta bloquear lo mismo en otra goroutine.
	type result struct {
		qty decimal.Decimal
		err error
	}
	done := make(chan result, 1)
	second, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Rollback()
	go func() {
		qty, err := postgres.NewStockBalanceStore(second).LockOnHand(ctx, productID)
		done <- result{qty, err}
	}()

	select {
	case r := <-done:
		t.Fatalf("second lock did not wait: got %s, %v", r.qty, r.err)
	case <-time.After(300 * time.Millisecond):
		// Sigue esperando: bien.
	}

	if _, err := postgres.NewStockBalanceStore(first).Add(ctx, productID, storeID, decimal.NewFromInt(5)); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := first.Commit(); err != nil {
		t.Fatalf("first commit: %v", err)
	}

	select {
	case r := <-done:
		if r.err != nil || !r.qty.Equal(decimal.NewFromInt(5)) {
			t.Errorf("second lock: got %s, %v, want 5 (the stock the first one left)", r.qty, r.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second lock still waiting after the first commit")
	}
}

// OnHandAt mira un solo local: el saldo de otro local no cuenta.
func TestStockBalanceStore_OnHandAt(t *testing.T) {
	f := newProductFixture(t)
	balances := postgres.NewStockBalanceStore(f.tx)
	p := f.create(t, "TEST-SB-9", "SALDO POR LOCAL TEST")
	f.addBalance(t, p.ID, "4") // otro local
	storeID := f.insertID(t, `INSERT INTO stores (name) VALUES ('Local test saldo') RETURNING id;`)

	if got, err := balances.OnHandAt(f.ctx, p.ID, storeID); err != nil || !got.IsZero() {
		t.Errorf("without balance: got %s, %v; want 0", got, err)
	}
	if _, err := balances.Add(f.ctx, p.ID, storeID, decimal.RequireFromString("2.5")); err != nil {
		t.Fatal(err)
	}
	if got, err := balances.OnHandAt(f.ctx, p.ID, storeID); err != nil || !got.Equal(decimal.RequireFromString("2.5")) {
		t.Errorf("with balance: got %s, %v; want 2.5", got, err)
	}
}
