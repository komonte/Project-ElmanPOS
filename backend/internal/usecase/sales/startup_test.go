package sales

import (
	"errors"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

// Modo arranque (0021): lo que el sistema no tiene se vende igual, el saldo
// queda en 0 y el resto queda en el renglón, sin movimiento.

func (f *fixture) startup(t *testing.T, on bool) {
	t.Helper()
	f.exec(t, `UPDATE app_settings SET value = to_jsonb($1::boolean) WHERE key = 'startup_mode';`, on)
}

func TestCheckoutStartupMode(t *testing.T) {
	f := newFixture(t)
	f.startup(t, true)
	empty := f.newProduct(t, "SIN STOCK TEST SALES", `'UNIT', 'UNIT', FALSE, TRUE`, "500", "0", "0")

	sale, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		Items: []*model.SaleItem{
			line(f.soda, "12"), // hay 10
			line(f.queso, "4"), // hay 3 kg: lote A 1, lote B 1,5 y 0,5 sin lote
			line(empty, "3"),   // no hay nada
		},
		Payments: []*model.Payment{pay(model.PaymentCash, "33500")},
	})
	if err != nil {
		t.Fatalf("Checkout in startup mode: %v", err)
	}

	wantUncovered := map[int64]string{f.soda: "2", f.queso: "1", empty: "3"}
	for _, it := range sale.Items {
		if !it.Uncovered.Equal(d(wantUncovered[it.ProductID])) {
			t.Errorf("%s: Uncovered = %s, want %s", it.ProductName, it.Uncovered, wantUncovered[it.ProductID])
		}
		var saved string
		f.scan(t, &saved, `SELECT uncovered_quantity::text FROM sale_items WHERE id = $1;`, it.ID)
		if !d(saved).Equal(d(wantUncovered[it.ProductID])) {
			t.Errorf("%s: saved uncovered_quantity = %s", it.ProductName, saved)
		}
	}
	// El saldo queda en 0, nunca negativo.
	for _, id := range []int64{f.soda, f.queso, empty} {
		if got := f.onHand(t, id); !got.IsZero() {
			t.Errorf("product %d: on hand = %s, want 0", id, got)
		}
	}
	// Los movimientos son solo lo que había: 10 de soda; del queso los dos
	// lotes y 0,5 sin lote; del vacío, ninguno.
	moved := func(id int64) string {
		var s string
		f.scan(t, &s, `SELECT COALESCE(sum(quantity), 0)::text FROM stock_movements WHERE sale_id = $1 AND product_id = $2;`,
			sale.ID, id)
		return s
	}
	for id, want := range map[int64]string{f.soda: "-10", f.queso: "-3", empty: "0"} {
		if got := moved(id); !d(got).Equal(d(want)) {
			t.Errorf("product %d: movements sum %s, want %s", id, got, want)
		}
	}
	if n := f.count(t, `SELECT count(*) FROM stock_movements WHERE sale_id = $1 AND product_id = $2 AND batch_id IS NULL;`,
		sale.ID, f.queso); n != 1 {
		t.Errorf("queso: %d movements without batch, want 1 (the 0,5 kg)", n)
	}

	// Anular devuelve solo lo que salió.
	if _, err := f.svc.Void(f.ctx, f.userID, sale.ID, "prueba"); err != nil {
		t.Fatalf("Void: %v", err)
	}
	for id, want := range map[int64]string{f.soda: "10", f.queso: "3", empty: "0"} {
		if got := f.onHand(t, id); !got.Equal(d(want)) {
			t.Errorf("after void, product %d: on hand = %s, want %s", id, got, want)
		}
	}
}

func TestCheckoutStartupModeOffStillRejects(t *testing.T) {
	f := newFixture(t)
	f.startup(t, false)
	_, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		Items:    []*model.SaleItem{line(f.soda, "11")},
		Payments: []*model.Payment{pay(model.PaymentCash, "11000")},
	})
	if !errors.Is(err, ErrNotEnoughStock) {
		t.Fatalf("got %v, want %v", err, ErrNotEnoughStock)
	}
	if got := f.onHand(t, f.soda); !got.Equal(d("10")) {
		t.Errorf("on hand = %s, want 10", got)
	}
}

// Lo que sí hay sale como siempre: en modo arranque, una venta cubierta no
// deja nada para contar.
func TestCheckoutStartupModeCovered(t *testing.T) {
	f := newFixture(t)
	f.startup(t, true)
	sale, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		Items:    []*model.SaleItem{line(f.soda, "4")},
		Payments: []*model.Payment{pay(model.PaymentCash, "4000")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sale.Items[0].Uncovered.IsZero() {
		t.Errorf("Uncovered = %s, want 0", sale.Items[0].Uncovered)
	}
	if got := f.onHand(t, f.soda); !got.Equal(d("6")) {
		t.Errorf("on hand = %s, want 6", got)
	}
}
