package postgres_test

import (
	"testing"
	"time"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Reportes de stock (R-01, R-02). La base puede tener otros productos: los
// controles miran solo los del test.

func TestReportStore_Restock(t *testing.T) {
	f := newProductFixture(t)
	reports := postgres.NewReportStore(f.tx)
	storeID := f.insertID(t, `INSERT INTO stores (name) VALUES ('Local test restock') RETURNING id;`)
	sess := f.openSession(t)

	// mk crea un producto con punto de pedido, máximo y stock.
	mk := func(sku, rp string, max any, stock string, supplier bool) int64 {
		p := f.create(t, sku, "PRODUCTO "+sku)
		exec(t, f.tx, `UPDATE products SET reorder_point = $2, max_stock = $3, preferred_supplier_id = $4 WHERE id = $1;`,
			p.ID, rp, max, map[bool]any{true: f.supplierID, false: nil}[supplier])
		if stock != "" {
			exec(t, f.tx, `INSERT INTO stock_balances (product_id, store_id, quantity, updated_at) VALUES ($1, $2, $3, now());`,
				p.ID, storeID, stock)
		}
		return p.ID
	}
	low := mk("TEST-RS-LOW", "5", "20", "2", true)     // bajo el punto
	at := mk("TEST-RS-AT", "5", nil, "5", false)       // justo en el punto
	ok := mk("TEST-RS-OK", "5", nil, "6", true)        // por encima: no
	none := mk("TEST-RS-NONE", "3", nil, "", true)     // nunca tuvo stock: 0 ≤ 3
	ranOut := mk("TEST-RS-RAN", "0", nil, "0", false)  // sin punto, agotado y con ventas
	never := mk("TEST-RS-NEVER", "0", nil, "", false)  // sin punto, nunca se vendió: no
	oldSale := mk("TEST-RS-OLD", "0", nil, "0", false) // vendido hace mucho: no
	inactive := mk("TEST-RS-OFF", "5", nil, "1", false)
	exec(t, f.tx, `UPDATE products SET is_active = FALSE WHERE id = $1;`, inactive)

	f.rawSale(t, sess.ID, time.Now().Add(-time.Hour), "100", false, []line{{ranOut, "4", "25", "10", "21"}, {low, "1.5", "100", "10", "21"}})
	f.rawSale(t, sess.ID, time.Now().Add(-time.Hour), "50", true, []line{{low, "7", "1", "1", "21"}}) // anulada: no cuenta
	f.rawSale(t, sess.ID, time.Now().AddDate(0, 0, -40), "100", false, []line{{oldSale, "1", "100", "10", "21"}})

	got, err := reports.Restock(f.ctx, time.Now().AddDate(0, 0, -30))
	if err != nil {
		t.Fatal(err)
	}
	byID := map[int64]model.RestockItem{}
	for _, it := range got {
		byID[it.ProductID] = it
	}
	for _, id := range []int64{low, at, none, ranOut} {
		if _, in := byID[id]; !in {
			t.Errorf("product %d missing", id)
		}
	}
	for _, id := range []int64{ok, never, oldSale, inactive} {
		if _, in := byID[id]; in {
			t.Errorf("product %d should not be there", id)
		}
	}
	l := byID[low]
	if !l.Quantity.Equal(d("2")) || !l.ReorderPoint.Equal(d("5")) || l.MaxStock == nil || !l.MaxStock.Equal(d("20")) ||
		!l.Sold.Equal(d("1.5")) || l.SupplierName == nil || *l.SupplierName != "Proveedor test product" || l.SKU != "TEST-RS-LOW" {
		t.Errorf("low: %+v", l)
	}
	if n := byID[none]; !n.Quantity.IsZero() || n.SupplierID == nil {
		t.Errorf("never stocked: %+v", n)
	}
	if r := byID[ranOut]; !r.Sold.Equal(d("4")) || r.SupplierID != nil || r.MaxStock != nil {
		t.Errorf("ran out: %+v", r)
	}
}

func TestReportStore_Expiring(t *testing.T) {
	f := newProductFixture(t)
	reports := postgres.NewReportStore(f.tx)
	p := f.create(t, "TEST-EXP", "PRODUCTO VENCE TEST")
	off := f.create(t, "TEST-EXP-OFF", "PRODUCTO VENCE INACTIVO")
	exec(t, f.tx, `UPDATE products SET controls_batches = TRUE WHERE id IN ($1, $2);`, p.ID, off.ID)
	exec(t, f.tx, `UPDATE products SET is_active = FALSE WHERE id = $1;`, off.ID)
	batch := func(product int64, number, expiry string, remaining string) int64 {
		var exp any
		if expiry != "" {
			exp = expiry
		}
		return f.insertID(t, `INSERT INTO product_batches (product_id, batch_number, expiry_date, original_quantity,
			remaining_quantity, unit_cost) VALUES ($1, $2, $3, 10, $4, 150) RETURNING id;`, product, number, exp, remaining)
	}
	expired := batch(p.ID, "L-VIEJO", "2031-01-10", "3")
	soon := batch(p.ID, "L-PRONTO", "2031-02-05", "2.5")
	limit := batch(off.ID, "L-LIMITE", "2031-02-09", "1")
	// No entran: vence después, ya no queda nada, no vence.
	batch(p.ID, "L-LEJOS", "2031-02-10", "4")
	batch(p.ID, "L-VACIO", "2031-01-05", "0")
	batch(p.ID, "L-SIN", "", "4")

	got, err := reports.Expiring(f.ctx, "2031-02-09")
	if err != nil {
		t.Fatal(err)
	}
	var mine []model.ExpiringBatch
	for _, b := range got {
		if b.ProductID == p.ID || b.ProductID == off.ID {
			mine = append(mine, b)
		}
	}
	if len(mine) != 3 || mine[0].BatchID != expired || mine[1].BatchID != soon || mine[2].BatchID != limit {
		t.Fatalf("expiring: got %+v", mine)
	}
	if mine[0].Expiry != "2031-01-10" || !mine[1].Remaining.Equal(d("2.5")) || !mine[1].UnitCost.Equal(d("150")) ||
		mine[1].BatchNumber != "L-PRONTO" || !mine[1].ProductActive || mine[2].ProductActive || mine[2].Name != "PRODUCTO VENCE INACTIVO" {
		t.Errorf("fields: %+v", mine)
	}
}
