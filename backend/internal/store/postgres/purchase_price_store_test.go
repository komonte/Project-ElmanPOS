package postgres_test

import (
	"testing"
	"time"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Precios por factura. La base puede tener otras compras y productos: los
// controles miran solo los del test.

func TestPurchasePriceStore(t *testing.T) {
	f := newProductFixture(t)
	prices := postgres.NewPurchasePriceStore(f.tx)
	storeID := f.insertID(t, `INSERT INTO stores (name) VALUES ('Local test precios') RETURNING id;`)
	received := time.Now().Add(-time.Hour)

	none := f.create(t, "TEST-PP-NONE", "PRODUCTO SIN PRECIO")
	old := f.create(t, "TEST-PP-OLD", "PRODUCTO PRECIO VIEJO")
	ok := f.create(t, "TEST-PP-OK", "PRODUCTO COTIZADO")
	off := f.create(t, "TEST-PP-OFF", "PRODUCTO INACTIVO")
	exec(t, f.tx, `UPDATE products SET is_active = FALSE WHERE id = $1;`, off.ID)
	exec(t, f.tx, `INSERT INTO prices (product_id, unit_price, valid_from) VALUES ($1, 900, $2);`, old.ID, received.Add(-time.Hour))
	exec(t, f.tx, `INSERT INTO prices (product_id, unit_price, valid_from) VALUES ($1, 1500, $2);`, ok.ID, received.Add(time.Minute))
	exec(t, f.tx, `INSERT INTO product_costs (product_id, unit_cost, avg_cost, valid_from) VALUES ($1, 700, 650, $2);`, old.ID, received)

	purchase := f.insertID(t, `INSERT INTO purchases (supplier_id, voucher_number, total, received_at)
		VALUES ($1, 'A-0001', 5000, $2) RETURNING id;`, f.supplierID, received)
	line := func(product int64, qty, cost string) {
		exec(t, f.tx, `INSERT INTO purchase_items (purchase_id, product_id, quantity, unit_cost) VALUES ($1, $2, $3, $4);`,
			purchase, product, qty, cost)
	}
	line(old.ID, "2", "600")
	line(none.ID, "3", "100")
	line(old.ID, "1", "700") // mismo producto en dos renglones: se suman, vale el último costo
	line(ok.ID, "5", "1000")
	line(off.ID, "1", "50")
	// Otra compra, ya cotizada.
	priced := f.insertID(t, `INSERT INTO purchases (supplier_id, total, received_at) VALUES ($1, 100, $2) RETURNING id;`,
		f.supplierID, received.Add(-2*time.Hour))
	exec(t, f.tx, `INSERT INTO purchase_items (purchase_id, product_id, quantity, unit_cost) VALUES ($1, $2, 1, 100);`, priced, ok.ID)

	t.Run("products of a purchase", func(t *testing.T) {
		got, err := prices.Products(f.ctx, purchase)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 4 || got[0].ProductID != old.ID || got[1].ProductID != none.ID || got[2].ProductID != ok.ID || got[3].ProductID != off.ID {
			t.Fatalf("order: %+v", got)
		}
		o := got[0]
		if !o.Quantity.Equal(d("3")) || !o.UnitCost.Equal(d("700")) || !o.AvgCost.Equal(d("650")) || o.Price == nil ||
			!o.Price.Equal(d("900")) || o.PriceFrom == nil || model.PriceStateAt(o.PriceFrom, received) != model.PriceOld {
			t.Errorf("old: %+v", o)
		}
		if n := got[1]; n.Price != nil || n.PriceFrom != nil || !n.AvgCost.IsZero() || !n.Active {
			t.Errorf("none: %+v", n)
		}
		if k := got[2]; model.PriceStateAt(k.PriceFrom, received) != model.PriceOK {
			t.Errorf("ok: %+v", k)
		}
		if got[3].Active {
			t.Error("off must be inactive")
		}
		if empty, err := prices.Products(f.ctx, -1); err != nil || len(empty) != 0 {
			t.Errorf("unknown purchase: %v, %v", empty, err)
		}
	})

	t.Run("list", func(t *testing.T) {
		all, err := prices.List(f.ctx, false, 1000)
		if err != nil {
			t.Fatal(err)
		}
		byID := map[int64]model.PurchaseSummary{}
		for _, p := range all {
			byID[p.ID] = p
		}
		p := byID[purchase]
		// Sin cotizar: el viejo y el sin precio. El cotizado y el inactivo no.
		if p.Products != 4 || p.Unpriced != 2 || p.SupplierName != "Proveedor test product" || p.VoucherNumber != "A-0001" ||
			!p.Total.Equal(d("5000")) || p.ReceivedByName != nil {
			t.Errorf("summary: %+v", p)
		}
		if q, in := byID[priced]; !in || q.Unpriced != 0 {
			t.Errorf("priced purchase: %+v", q)
		}
		only, err := prices.List(f.ctx, true, 1000)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range only {
			if x.ID == priced || x.Unpriced == 0 {
				t.Errorf("only unpriced has %+v", x)
			}
		}
		if n, err := prices.CountUnpriced(f.ctx); err != nil || n != len(only) {
			t.Errorf("count unpriced: %d (list %d), %v", n, len(only), err)
		}
	})

	t.Run("missing prices: with stock and no price", func(t *testing.T) {
		for _, id := range []int64{none.ID, old.ID, off.ID} {
			exec(t, f.tx, `INSERT INTO stock_balances (product_id, store_id, quantity, updated_at) VALUES ($1, $2, 4, now());`, id, storeID)
		}
		got, err := prices.MissingPrices(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := map[int64]model.PricedProduct{}
		for _, p := range got {
			found[p.ProductID] = p
		}
		// Con stock y sin precio: sí. Con precio, o inactivo, o sin stock: no.
		if n, in := found[none.ID]; !in || !n.Quantity.Equal(d("4")) {
			t.Errorf("none: %+v", n)
		}
		for _, id := range []int64{old.ID, off.ID, ok.ID} {
			if _, in := found[id]; in {
				t.Errorf("product %d should not be missing", id)
			}
		}
		if n, err := prices.CountMissingPrices(f.ctx); err != nil || n != len(got) {
			t.Errorf("count missing: %d (list %d), %v", n, len(got), err)
		}
	})
}
