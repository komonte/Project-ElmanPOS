package reports

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// Vendidos sin stock (0021): quedan pendientes hasta que un conteo
// confirmado saca la foto del producto después de la venta. Las anuladas no
// cuentan.
func TestUnstocked(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	store := f.id(t, `SELECT store_id FROM cash_registers WHERE id = $1;`, f.register)
	s := f.session(t, now.Add(-5*time.Hour), "0")
	uncovered := func(product int64, at time.Time, voided bool, qty, missing string) {
		f.sale(t, s, at, voided, []item{{product, qty, "100", "0", "21"}}, map[string]string{"CASH": "1000"})
		f.exec(t, `UPDATE sale_items SET uncovered_quantity = $3
			WHERE product_id = $1 AND sale_id = (SELECT id FROM sales WHERE cashier_session_id = $2 ORDER BY id DESC LIMIT 1);`,
			product, s, missing)
	}
	count := func(product int64, snapshot time.Time) {
		c := f.id(t, `INSERT INTO stock_counts (store_id, status, confirmed_at) VALUES ($1, 'CONFIRMED', now()) RETURNING id;`, store)
		f.exec(t, `INSERT INTO stock_count_items (count_id, product_id, system_quantity, counted_quantity, created_at)
			VALUES ($1, $2, 0, 5, $3);`, c, product, snapshot)
	}

	// Unidad: dos ventas pendientes y una anulada. Un conteo viejo (foto
	// antes de las ventas) no las resuelve.
	count(f.unit, now.Add(-4*time.Hour))
	uncovered(f.unit, now.Add(-3*time.Hour), false, "3", "2")
	uncovered(f.unit, now.Add(-time.Hour), false, "1", "1")
	uncovered(f.unit, now.Add(-2*time.Hour), true, "5", "5")
	// Peso: vendido sin stock y después contado: resuelto.
	uncovered(f.weight, now.Add(-3*time.Hour), false, "1", "0.5")
	count(f.weight, now.Add(-2*time.Hour))

	items, err := f.svc.Unstocked(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]decimal.Decimal{}
	for _, it := range items {
		if it.ProductID == f.unit {
			if it.Sales != 2 || !it.Uncovered.Equal(d("3")) || it.FirstSold == "" || it.LastSold == "" {
				t.Errorf("unit: %+v", it)
			}
		}
		got[it.ProductID] = it.Uncovered
	}
	if _, in := got[f.unit]; !in {
		t.Error("unit: missing, it was sold without stock after the last count")
	}
	if _, in := got[f.weight]; in {
		t.Error("weight: listed, but a count measured it after the sale")
	}

	pending, err := f.svc.UnstockedFor(f.ctx, []int64{f.unit, f.weight})
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || !pending[f.unit].Equal(d("3")) {
		t.Errorf("UnstockedFor: %v", pending)
	}
	if empty, err := f.svc.UnstockedFor(f.ctx, nil); err != nil || len(empty) != 0 {
		t.Errorf("UnstockedFor(nil): %v, %v", empty, err)
	}
}

// Códigos cobrados como artículo sin registrar (0022): salen hasta que se
// dan de alta; las ventas anuladas no cuentan.
func TestUnregisteredCodes(t *testing.T) {
	f := newFixture(t)
	now := time.Now()
	s := f.session(t, now.Add(-5*time.Hour), "0")
	sold := func(at time.Time, voided bool, code, price string) {
		f.sale(t, s, at, voided, []item{{f.unit, "1", price, "0", "21"}}, map[string]string{"CASH": price})
		f.exec(t, `UPDATE sale_items SET scanned_code = $2
			WHERE sale_id = (SELECT id FROM sales WHERE cashier_session_id = $1 ORDER BY id DESC LIMIT 1);`, s, code)
	}
	sold(now.Add(-3*time.Hour), false, "2999000000101", "300")
	sold(now.Add(-time.Hour), false, "2999000000101", "350")
	sold(now.Add(-2*time.Hour), true, "2999000000102", "100")  // anulada
	sold(now.Add(-2*time.Hour), false, "2999000000103", "100") // y después se da de alta
	f.exec(t, `INSERT INTO product_codes (product_id, code_type, code) VALUES ($1, 'INTERNAL', '2999000000103');`, f.weight)

	codes, err := f.svc.UnregisteredCodes(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	mine := map[string]bool{}
	for _, c := range codes {
		mine[c.Code] = true
		if c.Code == "2999000000101" && (c.Sales != 2 || !c.LastPrice.Equal(d("350")) || c.SoldAs != "UNIDAD TEST") {
			t.Errorf("2999000000101: %+v", c)
		}
	}
	if !mine["2999000000101"] || mine["2999000000102"] || mine["2999000000103"] {
		t.Errorf("codes: %v", mine)
	}
}
