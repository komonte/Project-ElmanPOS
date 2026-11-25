package postgres_test

import (
	"testing"

	"github.com/shopspring/decimal"

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
