package sales

import (
	"errors"
	"testing"
	"time"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Combos (0023): el renglón es el combo; el stock sale de sus componentes.

// comboFixture agrega un cigarrillo suelto (por unidad, $150, costo 100,
// stock 50) y un atado: combo de 20 sueltos a $2500.
func comboFixture(t *testing.T) (f *fixture, suelto, atado int64) {
	t.Helper()
	f = newFixture(t)
	suelto = f.newProduct(t, "SUELTO TEST SALES", `'UNIT', 'UNIT', FALSE, TRUE`, "150", "100", "50")
	atado = f.newProduct(t, "ATADO TEST SALES", `'COMPOSITE', 'UNIT', FALSE, TRUE`, "2500", "0", "0")
	f.component(t, atado, suelto, "20")
	return f, suelto, atado
}

func (f *fixture) component(t *testing.T, pack, comp int64, qty string) {
	t.Helper()
	f.exec(t, `INSERT INTO product_components (pack_id, component_id, quantity) VALUES ($1, $2, $3);`, pack, comp, qty)
}

func (f *fixture) moved(t *testing.T, saleID, productID int64) string {
	t.Helper()
	var s string
	f.scan(t, &s, `SELECT COALESCE(sum(quantity), 0)::text FROM stock_movements WHERE sale_id = $1 AND product_id = $2;`,
		saleID, productID)
	return s
}

func TestCheckoutCombo(t *testing.T) {
	f, suelto, atado := comboFixture(t)
	sale, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		Items:    []*model.SaleItem{line(atado, "2"), line(suelto, "3")},
		Payments: []*model.Payment{pay(model.PaymentCash, "5450")},
	})
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if !sale.Total.Equal(d("5450")) {
		t.Errorf("Total = %s, want 5450 (2 × 2500 + 3 × 150)", sale.Total)
	}
	combo := sale.Items[0]
	if !combo.UnitPrice.Equal(d("2500")) || !combo.UnitCost.Equal(d("2000")) {
		t.Errorf("combo line: price %s cost %s, want 2500 and 2000 (20 × 100)", combo.UnitPrice, combo.UnitCost)
	}
	// 40 sueltos para los atados y 3 sueltos: 43 de 50.
	if got := f.onHand(t, suelto); !got.Equal(d("7")) {
		t.Errorf("suelto on hand = %s, want 7", got)
	}
	if got := f.onHand(t, atado); !got.IsZero() {
		t.Errorf("the combo has no stock of its own: on hand = %s", got)
	}
	if got := f.moved(t, sale.ID, suelto); !d(got).Equal(d("-43")) {
		t.Errorf("suelto movements = %s, want -43", got)
	}
	if n := f.count(t, `SELECT count(*) FROM stock_movements WHERE product_id = $1;`, atado); n != 0 {
		t.Errorf("the combo got %d movements, want 0", n)
	}
	var qty, cost, uncovered string
	f.scan(t, &qty, `SELECT quantity::text FROM sale_item_components WHERE sale_item_id = $1 AND product_id = $2;`,
		combo.ID, suelto)
	f.scan(t, &cost, `SELECT unit_cost::text FROM sale_item_components WHERE sale_item_id = $1;`, combo.ID)
	f.scan(t, &uncovered, `SELECT uncovered_quantity::text FROM sale_item_components WHERE sale_item_id = $1;`, combo.ID)
	if !d(qty).Equal(d("40")) || !d(cost).Equal(d("100")) || !d(uncovered).IsZero() {
		t.Errorf("sale_item_components: qty %s cost %s uncovered %s, want 40, 100, 0", qty, cost, uncovered)
	}

	// Reposición cuenta los atados como sueltos vendidos: 40 + 3.
	f.exec(t, `UPDATE products SET reorder_point = 20 WHERE id = $1;`, suelto)
	restock, err := postgres.NewReportStore(f.tx).Restock(f.ctx, time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range restock {
		if r.ProductID == suelto {
			found = true
			if !r.Sold.Equal(d("43")) {
				t.Errorf("restock: suelto sold = %s, want 43", r.Sold)
			}
		}
		if r.ProductID == atado {
			t.Error("restock lists the combo: it has no stock of its own")
		}
	}
	if !found {
		t.Error("restock does not list the loose product")
	}

	// Anular devuelve los sueltos.
	if _, err := f.svc.Void(f.ctx, f.userID, sale.ID, "prueba"); err != nil {
		t.Fatalf("Void: %v", err)
	}
	if got := f.onHand(t, suelto); !got.Equal(d("50")) {
		t.Errorf("after void, suelto on hand = %s, want 50", got)
	}
}

// Lo que piden el combo y el suelto se suma: 2 atados (40) y 11 sueltos son
// 51, y hay 50. El que se queda sin stock es el renglón que llega tarde.
func TestCheckoutComboSharesStock(t *testing.T) {
	f, suelto, atado := comboFixture(t)
	_, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		Items:    []*model.SaleItem{line(atado, "2"), line(suelto, "11")},
		Payments: []*model.Payment{pay(model.PaymentCash, "10000")},
	})
	var le *LineError
	if !errors.Is(err, ErrNotEnoughStock) || !errors.As(err, &le) {
		t.Fatalf("err = %v, want ErrNotEnoughStock", err)
	}
	if le.ProductID != suelto || le.Component != "" || !le.Available.Equal(d("10")) {
		t.Errorf("LineError = %+v, want the loose line with 10 left", le)
	}
	if got := f.onHand(t, suelto); !got.Equal(d("50")) {
		t.Errorf("rejected sale moved stock: on hand = %s", got)
	}
}

func TestCheckoutComboNotEnough(t *testing.T) {
	f, _, atado := comboFixture(t)
	_, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		Items:    []*model.SaleItem{line(atado, "3")}, // 60 sueltos, hay 50
		Payments: []*model.Payment{pay(model.PaymentCash, "7500")},
	})
	var le *LineError
	if !errors.Is(err, ErrNotEnoughStock) || !errors.Is(err, service.ErrConflict) || !errors.As(err, &le) {
		t.Fatalf("err = %v, want ErrNotEnoughStock", err)
	}
	if le.ProductID != atado || le.Component != "SUELTO TEST SALES" || !le.Available.Equal(d("50")) {
		t.Errorf("LineError = %+v, want the combo, naming the loose one, with 50", le)
	}
}

// En modo arranque lo que falta de un componente queda en su renglón de
// sale_item_components, va a "vendidos sin stock" como del componente y
// anular devuelve solo lo que salió.
func TestCheckoutComboStartupMode(t *testing.T) {
	f, suelto, atado := comboFixture(t)
	f.startup(t, true)
	sale, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		Items:    []*model.SaleItem{line(atado, "3")},
		Payments: []*model.Payment{pay(model.PaymentCash, "7500")},
	})
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	c := sale.Items[0].Components[0]
	if !c.Quantity.Equal(d("60")) || !c.Uncovered.Equal(d("10")) {
		t.Errorf("component: qty %s uncovered %s, want 60 and 10", c.Quantity, c.Uncovered)
	}
	if !sale.Items[0].Uncovered.IsZero() {
		t.Errorf("the combo line itself has uncovered %s, want 0", sale.Items[0].Uncovered)
	}
	if got := f.onHand(t, suelto); !got.IsZero() {
		t.Errorf("suelto on hand = %s, want 0", got)
	}
	if got := f.moved(t, sale.ID, suelto); !d(got).Equal(d("-50")) {
		t.Errorf("suelto movements = %s, want -50 (only what there was)", got)
	}
	pending, err := postgres.NewReportStore(f.tx).UnstockedFor(f.ctx, []int64{suelto, atado})
	if err != nil {
		t.Fatal(err)
	}
	if got := pending[suelto]; !got.Equal(d("10")) {
		t.Errorf("unstocked suelto = %s, want 10", got)
	}
	if _, ok := pending[atado]; ok {
		t.Error("the combo itself is never pending: it has no stock")
	}

	if _, err := f.svc.Void(f.ctx, f.userID, sale.ID, "prueba"); err != nil {
		t.Fatalf("Void: %v", err)
	}
	if got := f.onHand(t, suelto); !got.Equal(d("50")) {
		t.Errorf("after void, suelto on hand = %s, want 50", got)
	}
	pending, err = postgres.NewReportStore(f.tx).UnstockedFor(f.ctx, []int64{suelto})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := pending[suelto]; ok {
		t.Error("a voided sale is not pending")
	}
}

// Un componente con lotes sale FIFO; los lotes quedan en el renglón del
// combo y el costo es el de los lotes.
func TestCheckoutComboBatches(t *testing.T) {
	f := newFixture(t)
	picada := f.newProduct(t, "PICADA TEST SALES", `'COMPOSITE', 'UNIT', FALSE, TRUE`, "4000", "0", "0")
	f.component(t, picada, f.queso, "0.5")
	f.component(t, picada, f.soda, "1")
	sale, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		Items:    []*model.SaleItem{line(picada, "3")}, // 1,5 kg: lote A 1 y lote B 0,5; 3 sodas
		Payments: []*model.Payment{pay(model.PaymentCash, "12000")},
	})
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	it := sale.Items[0]
	if len(it.Batches) != 2 {
		t.Fatalf("combo line batches = %d, want 2", len(it.Batches))
	}
	// (1 × 3000 + 0,5 × 3200 + 3 × 600) / 3 = 6400 / 3
	if !it.UnitCost.Equal(d("2133.33")) {
		t.Errorf("combo unit cost = %s, want 2133.33", it.UnitCost)
	}
	if n := f.count(t, `SELECT count(*) FROM sale_item_batches WHERE sale_item_id = $1;`, it.ID); n != 2 {
		t.Errorf("saved %d sale_item_batches, want 2", n)
	}
	if got := f.onHand(t, f.queso); !got.Equal(d("1.5")) {
		t.Errorf("queso on hand = %s, want 1.5", got)
	}
	var remA string
	f.scan(t, &remA, `SELECT remaining_quantity::text FROM product_batches WHERE id = $1;`, f.batchA)
	if !d(remA).IsZero() {
		t.Errorf("batch A remaining = %s, want 0", remA)
	}

	if _, err := f.svc.Void(f.ctx, f.userID, sale.ID, "prueba"); err != nil {
		t.Fatalf("Void: %v", err)
	}
	f.scan(t, &remA, `SELECT remaining_quantity::text FROM product_batches WHERE id = $1;`, f.batchA)
	if !d(remA).Equal(d("1")) || !f.onHand(t, f.queso).Equal(d("3")) {
		t.Errorf("after void: batch A %s, queso %s; want 1 and 3", remA, f.onHand(t, f.queso))
	}
}

// Una receta vieja (el componente se desactivó después) no se vende.
func TestCheckoutComboInactiveComponent(t *testing.T) {
	f, suelto, atado := comboFixture(t)
	f.exec(t, `UPDATE products SET is_active = FALSE WHERE id = $1;`, suelto)
	_, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		Items:    []*model.SaleItem{line(atado, "1")},
		Payments: []*model.Payment{pay(model.PaymentCash, "2500")},
	})
	var le *LineError
	if !errors.Is(err, ErrComboComponent) || !errors.Is(err, model.ErrComponentInactive) || !errors.As(err, &le) {
		t.Fatalf("err = %v, want ErrComboComponent / ErrComponentInactive", err)
	}
	if le.ProductID != atado || le.Component != "SUELTO TEST SALES" {
		t.Errorf("LineError = %+v", le)
	}
}

// En la caja el combo aparece con cuántos alcanzan: 50 sueltos son 2
// atados.
func TestSearchCombo(t *testing.T) {
	f, _, _ := comboFixture(t)
	got, err := f.svc.Search(f.ctx, "TEST SALES")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Option{}
	for _, o := range got {
		byName[o.Name] = o
	}
	a, ok := byName["ATADO TEST SALES"]
	if !ok || !a.OnHand.Equal(d("2")) || a.Price == nil || !a.Price.Equal(d("2500")) {
		t.Errorf("atado: %+v (found %v), want on hand 2 and price 2500", a, ok)
	}
	if c, ok := byName["COMBO TEST SALES"]; !ok || !c.OnHand.IsZero() {
		t.Errorf("combo without components: %+v (found %v), want on hand 0", c, ok)
	}
}
