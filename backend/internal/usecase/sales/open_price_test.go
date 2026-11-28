package sales

import (
	"errors"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

// Artículo sin registrar (0022): el precio lo escribe la caja, no mueve
// stock y guarda el código escaneado.
func TestCheckoutOpenPrice(t *testing.T) {
	f := newFixture(t)
	varios := f.newProduct(t, "VARIOS TEST SALES", `'UNIT', 'UNIT', FALSE, TRUE`, "", "0", "0")
	f.exec(t, `UPDATE products SET open_price = TRUE WHERE id = $1;`, varios)

	sale, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		Items: []*model.SaleItem{
			{ProductID: varios, Quantity: d("2"), UnitPrice: d("350.004"), ScannedCode: " 7790000000017 "},
			{ProductID: varios, Quantity: d("1"), UnitPrice: d("120")},
			// Un producto común ignora el precio y el código que manda la caja.
			{ProductID: f.soda, Quantity: d("1"), UnitPrice: d("1"), ScannedCode: "123"},
		},
		Payments: []*model.Payment{pay(model.PaymentCash, "2000")},
	})
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if !sale.Total.Equal(d("1820")) { // 700 + 120 + 1000
		t.Errorf("total = %s, want 1820", sale.Total)
	}
	type row struct {
		price, cost, code string
	}
	var got []row
	rows, err := f.tx.QueryContext(f.ctx, `SELECT unit_price::text, unit_cost::text, COALESCE(scanned_code, '')
		FROM sale_items WHERE sale_id = $1 ORDER BY id;`, sale.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.price, &r.cost, &r.code); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	rows.Close()
	want := []row{{"350.00", "0.00", "7790000000017"}, {"120.00", "0.00", ""}, {"1000.00", "600.00", ""}}
	if len(got) != len(want) {
		t.Fatalf("rows: %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d: got %+v, want %+v", i+1, got[i], want[i])
		}
	}
	// Ni movimientos ni saldo del artículo sin registrar.
	if n := f.count(t, `SELECT count(*) FROM stock_movements WHERE product_id = $1;`, varios); n != 0 {
		t.Errorf("open-price movements: %d", n)
	}
	if got := f.onHand(t, varios); !got.IsZero() {
		t.Errorf("open-price on hand: %s", got)
	}
	// Anular no devuelve nada de él.
	if _, err := f.svc.Void(f.ctx, f.userID, sale.ID, "prueba"); err != nil {
		t.Fatal(err)
	}
	if got := f.onHand(t, varios); !got.IsZero() {
		t.Errorf("after void, open-price on hand: %s", got)
	}
}

func TestCheckoutOpenPriceNeedsAPrice(t *testing.T) {
	f := newFixture(t)
	varios := f.newProduct(t, "VARIOS TEST SALES", `'UNIT', 'UNIT', FALSE, TRUE`, "", "0", "0")
	f.exec(t, `UPDATE products SET open_price = TRUE WHERE id = $1;`, varios)
	_, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		Items:    []*model.SaleItem{{ProductID: varios, Quantity: d("1")}},
		Payments: []*model.Payment{pay(model.PaymentCash, "100")},
	})
	var line *LineError
	if !errors.Is(err, ErrOpenPriceMissing) || !errors.As(err, &line) || line.ProductName != "VARIOS TEST SALES" {
		t.Fatalf("got %v, want %v naming the product", err, ErrOpenPriceMissing)
	}
}

func TestOpenItems(t *testing.T) {
	f := newFixture(t)
	varios := f.newProduct(t, "VARIOS TEST SALES", `'UNIT', 'UNIT', FALSE, TRUE`, "", "0", "0")
	off := f.newProduct(t, "VARIOS INACTIVO TEST SALES", `'UNIT', 'UNIT', FALSE, FALSE`, "", "0", "0")
	f.exec(t, `UPDATE products SET open_price = TRUE WHERE id IN ($1, $2);`, varios, off)
	items, err := f.svc.OpenItems(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := map[int64]bool{}
	for _, o := range items {
		found[o.ID] = o.OpenPrice
	}
	if !found[varios] || found[off] {
		t.Errorf("OpenItems: %+v", items)
	}
	opts, err := f.svc.Search(f.ctx, "VARIOS TEST SALES")
	if err != nil || len(opts) == 0 || !opts[0].OpenPrice {
		t.Errorf("Search: %+v, %v", opts, err)
	}
}

// Un producto común no puede ir en dos renglones aunque la caja mande un
// precio: el modelo lo deja pasar por el precio, el caso de uso no.
func TestCheckoutDuplicateCommonProduct(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		Items: []*model.SaleItem{
			{ProductID: f.soda, Quantity: d("1"), UnitPrice: d("5")},
			{ProductID: f.soda, Quantity: d("1"), UnitPrice: d("5")},
		},
		Payments: []*model.Payment{pay(model.PaymentCash, "2000")},
	})
	if !errors.Is(err, model.ErrSaleDuplicateProduct) {
		t.Fatalf("got %v, want %v", err, model.ErrSaleDuplicateProduct)
	}
	if got := f.onHand(t, f.soda); !got.Equal(d("10")) {
		t.Errorf("on hand = %s, want 10", got)
	}
}
