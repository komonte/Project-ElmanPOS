package reports

import (
	"testing"
	"time"
)

// La base puede tener otros productos y lotes: se miran solo los del test.

func TestRestock(t *testing.T) {
	f := newFixture(t)
	supA := f.id(t, `INSERT INTO suppliers (name) VALUES ('zeta distribuidora test') RETURNING id;`)
	supB := f.id(t, `INSERT INTO suppliers (name) VALUES ('Alfa Test') RETURNING id;`)
	store := f.id(t, `SELECT store_id FROM cash_registers WHERE id = $1;`, f.register)
	cat := f.id(t, `SELECT category_id FROM products WHERE id = $1;`, f.unit)
	mk := func(sku, rp, stock string, supplier any) int64 {
		id := f.id(t, `INSERT INTO products (sku, name, category_id, reorder_point, preferred_supplier_id)
			VALUES ($1, $1, $2, $3, $4) RETURNING id;`, sku, cat, rp, supplier)
		f.exec(t, `INSERT INTO stock_balances (product_id, store_id, quantity, updated_at) VALUES ($1, $2, $3, now());`, id, store, stock)
		return id
	}
	a1 := mk("TEST-R-A1", "10", "5", supA) // 0,5
	a2 := mk("TEST-R-A2", "10", "1", supA) // 0,1: primero
	b1 := mk("TEST-R-B1", "4", "4", supB)
	n1 := mk("TEST-R-N1", "2", "0", nil)
	ran := mk("TEST-R-RAN", "0", "0", nil)
	s := f.session(t, time.Now().Add(-2*time.Hour), "0")
	f.sale(t, s, time.Now().Add(-time.Hour), false, []item{{ran, "3", "100", "50", "21"}}, map[string]string{"CASH": "300"})

	r, err := f.svc.Restock(f.ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Los grupos de los proveedores del test, en orden; sin proveedor al final.
	var order []string
	pos := map[int64]int{}
	for _, g := range r.Below {
		for i, it := range g.Items {
			pos[it.ProductID] = i
		}
		if g.SupplierID != nil && (*g.SupplierID == supA || *g.SupplierID == supB) {
			order = append(order, g.SupplierName)
		}
	}
	if len(order) != 2 || order[0] != "Alfa Test" || order[1] != "zeta distribuidora test" {
		t.Errorf("groups: %v", order)
	}
	last := r.Below[len(r.Below)-1]
	if last.SupplierID != nil || last.SupplierName != "" {
		t.Errorf("no supplier must go last: %+v", last.SupplierName)
	}
	found := false
	for _, it := range last.Items {
		found = found || it.ProductID == n1
	}
	if !found {
		t.Error("n1 must be in the no-supplier group")
	}
	if pos[a2] >= pos[a1] {
		t.Errorf("most critical first: a2 at %d, a1 at %d", pos[a2], pos[a1])
	}
	if _, in := pos[b1]; !in {
		t.Error("b1 (at the reorder point) missing")
	}
	ranOut := false
	for _, it := range r.RanOut {
		if it.ProductID == ran {
			ranOut = it.Sold.Equal(d("3"))
		}
	}
	if !ranOut {
		t.Errorf("ran out: %+v", r.RanOut)
	}
	if r.Count() < 5 {
		t.Errorf("count: %d", r.Count())
	}
}

func TestExpiring(t *testing.T) {
	f := newFixture(t)
	f.exec(t, `UPDATE products SET controls_batches = TRUE WHERE id = $1;`, f.unit)
	batch := func(number, expiry, remaining, cost string) {
		f.exec(t, `INSERT INTO product_batches (product_id, batch_number, expiry_date, original_quantity, remaining_quantity, unit_cost)
			VALUES ($1, $2, $3, 100, $4, $5);`, f.unit, number, expiry, remaining, cost)
	}
	batch("L-AYER", "2031-03-09", "2", "100")
	batch("L-HOY", "2031-03-10", "1", "50")
	batch("L-EN7", "2031-03-17", "3", "10")
	batch("L-EN8", "2031-03-18", "3", "10")

	today := time.Date(2031, 3, 10, 22, 0, 0, 0, f.ba) // la noche del 10 en el local
	r, err := f.svc.Expiring(f.ctx, today, 7)
	if err != nil {
		t.Fatal(err)
	}
	mine := func(rows []ExpiringRow) []ExpiringRow {
		var out []ExpiringRow
		for _, row := range rows {
			if row.ProductID == f.unit {
				out = append(out, row)
			}
		}
		return out
	}
	expired, soon := mine(r.Expired), mine(r.Soon)
	if len(expired) != 1 || expired[0].BatchNumber != "L-AYER" || expired[0].DaysLeft != -1 {
		t.Errorf("expired: %+v", expired)
	}
	if len(soon) != 2 || soon[0].BatchNumber != "L-HOY" || soon[0].DaysLeft != 0 || soon[1].DaysLeft != 7 {
		t.Errorf("soon: %+v", soon)
	}
	if r.Days != 7 || r.SoonValue.LessThan(d("80")) {
		t.Errorf("days %d, soon value %v", r.Days, r.SoonValue)
	}
}

func TestExpiryDays(t *testing.T) {
	f := newFixture(t)
	f.exec(t, `DELETE FROM app_settings WHERE key = 'expiry_warning_days';`)
	if got := f.svc.ExpiryDays(f.ctx); got != DefaultExpiryDays {
		t.Errorf("missing: %d", got)
	}
	for value, want := range map[string]int{`15`: 15, `"15"`: DefaultExpiryDays, `0`: DefaultExpiryDays, `2.5`: DefaultExpiryDays, `9999`: DefaultExpiryDays} {
		f.exec(t, `INSERT INTO app_settings (key, value) VALUES ('expiry_warning_days', $1::jsonb)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value;`, value)
		if got := f.svc.ExpiryDays(f.ctx); got != want {
			t.Errorf("%s: got %d, want %d", value, got, want)
		}
	}
}
