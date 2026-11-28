package sales

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	_ "github.com/lib/pq"
	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/usecasetest"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// fixture: una transacción que se deshace al terminar, con un solo local
// activo, su caja abierta y estos productos (IVA 21 %):
//   - soda: por unidad, sin lotes, $1000, costo promedio 600, stock 10;
//   - queso: por peso, con lotes, $5000 el kg, promedio 3100, stock 3 kg:
//     lote A 1 kg a 3000 (vence antes), lote B 1,5 kg a 3200 y 0,5 kg sin
//     lote;
//   - sin precio, inactivo y combo, con stock.
type fixture struct {
	ctx        context.Context
	tx         *sql.Tx
	userID     int64 // quién hace las cosas (F-13)
	svc        *Service
	storeID    int64
	categoryID int64
	registerID int64
	sessionID  int64

	soda, queso, noPrice, inactive, composite int64
	batchA, batchB                            int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; run `make test` with the DB up")
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx (is the DB up?): %v", err)
	}
	t.Cleanup(func() { tx.Rollback() })

	f := &fixture{ctx: context.Background(), tx: tx, svc: New(tx, postgres.NewSavepointRunner(tx))}
	if err := tx.QueryRowContext(f.ctx, `INSERT INTO users (username, name, password_hash, role)
		VALUES ('test.usuario', 'Usuario Test', 'x', 'ADMIN') RETURNING id;`).Scan(&f.userID); err != nil {
		t.Fatal(err)
	}
	f.exec(t, `UPDATE stores SET is_active = FALSE;`)
	f.scan(t, &f.storeID, `INSERT INTO stores (name) VALUES ('Local test sales') RETURNING id;`)
	var taxID int64
	f.scan(t, &taxID, `INSERT INTO taxes (name, rate) VALUES ('IVA test sales', 21) RETURNING id;`)
	f.scan(t, &f.categoryID, `INSERT INTO categories (name, tax_id) VALUES ('Categoria test sales', $1) RETURNING id;`, taxID)
	f.scan(t, &f.registerID, `INSERT INTO cash_registers (store_id, name) VALUES ($1, 'Caja test') RETURNING id;`, f.storeID)
	f.scan(t, &f.sessionID, `INSERT INTO cashier_sessions (cash_register_id, opening_amount) VALUES ($1, 0) RETURNING id;`, f.registerID)

	f.soda = f.newProduct(t, "SODA TEST SALES", `'UNIT', 'UNIT', FALSE, TRUE`, "1000", "600", "10")
	f.queso = f.newProduct(t, "QUESO TEST SALES", `'WEIGHT', 'KILOGRAM', TRUE, TRUE`, "5000", "3100", "3")
	f.scan(t, &f.batchA, `INSERT INTO product_batches (product_id, batch_number, expiry_date, original_quantity,
		remaining_quantity, unit_cost) VALUES ($1, 'A', '2030-01-01', 1, 1, 3000) RETURNING id;`, f.queso)
	f.scan(t, &f.batchB, `INSERT INTO product_batches (product_id, batch_number, expiry_date, original_quantity,
		remaining_quantity, unit_cost) VALUES ($1, 'B', '2030-06-01', 1.5, 1.5, 3200) RETURNING id;`, f.queso)
	f.noPrice = f.newProduct(t, "SIN PRECIO TEST SALES", `'UNIT', 'UNIT', FALSE, TRUE`, "", "100", "5")
	f.inactive = f.newProduct(t, "INACTIVO TEST SALES", `'UNIT', 'UNIT', FALSE, FALSE`, "100", "50", "5")
	f.composite = f.newProduct(t, "COMBO TEST SALES", `'COMPOSITE', 'UNIT', FALSE, TRUE`, "100", "50", "5")
	return f
}

func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.tx.ExecContext(f.ctx, q, args...); err != nil {
		t.Fatalf("fixture %q: %v", q, err)
	}
}

func (f *fixture) scan(t *testing.T, dest any, q string, args ...any) {
	t.Helper()
	if err := f.tx.QueryRowContext(f.ctx, q, args...).Scan(dest); err != nil {
		t.Fatalf("fixture %q: %v", q, err)
	}
}

// newProduct crea un producto con precio (vacío = sin precio), costo
// promedio y stock en el local.
func (f *fixture) newProduct(t *testing.T, name, values, price, avg, stock string) int64 {
	t.Helper()
	var id int64
	f.scan(t, &id, `INSERT INTO products (name, category_id, sale_type, measure_unit, controls_batches,
		is_active) VALUES ($1, $2, `+values+`) RETURNING id;`, name, f.categoryID)
	if price != "" {
		f.exec(t, `INSERT INTO prices (product_id, unit_price, valid_from) VALUES ($1, $2, now());`, id, price)
	}
	f.exec(t, `INSERT INTO product_costs (product_id, unit_cost, avg_cost, valid_from) VALUES ($1, $2, $2, now());`, id, avg)
	f.exec(t, `INSERT INTO stock_balances (product_id, store_id, quantity) VALUES ($1, $2, $3);`, id, f.storeID, stock)
	return id
}

func (f *fixture) onHand(t *testing.T, productID int64) decimal.Decimal {
	t.Helper()
	var q decimal.Decimal
	f.scan(t, &q, `SELECT quantity FROM stock_balances WHERE product_id = $1 AND store_id = $2;`, productID, f.storeID)
	return q
}

func (f *fixture) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	f.scan(t, &n, q, args...)
	return n
}

func line(productID int64, qty string) *model.SaleItem {
	return &model.SaleItem{ProductID: productID, Quantity: d(qty)}
}

func pay(method, amount string) *model.Payment {
	return &model.Payment{Method: method, Amount: d(amount)}
}

func TestCheckout(t *testing.T) {
	f := newFixture(t)
	in := &model.Sale{
		// El precio y el costo que manda la caja se ignoran.
		Items: []*model.SaleItem{
			{ProductID: f.soda, Quantity: d("2"), UnitPrice: d("1"), UnitCost: d("1")},
			line(f.queso, "2.700"),
		},
		Payments: []*model.Payment{
			{Method: model.PaymentTransfer, Amount: d("5000"), Reference: " op 9 "},
			pay(model.PaymentCash, "11000"),
		},
	}
	sale, err := f.svc.Checkout(f.ctx, f.userID, in)
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	usecasetest.WantAuthor(t, f.ctx, f.tx, "charged by", usecasetest.Author("sales", "created_by", "id = $2"), f.userID, sale.ID)
	usecasetest.WantAuthor(t, f.ctx, f.tx, "sale movements", usecasetest.Author("stock_movements", "performed_by", "sale_id = $2"), f.userID, sale.ID)
	if !in.Items[0].UnitPrice.Equal(d("1")) || !in.Payments[1].Amount.Equal(d("11000")) {
		t.Error("checkout modified its input")
	}

	// 2 × 1000 + 2,7 × 5000 = 15500; IVA 15500 × 21 / 121 = 2690,08.
	if sale.ID == 0 || sale.SaleNumber != 1 || sale.CashierSessionID != f.sessionID || sale.RegisterName != "Caja test" ||
		!sale.Total.Equal(d("15500")) || !sale.Subtotal.Equal(d("15500")) || !sale.TaxAmount.Equal(d("2690.08")) {
		t.Errorf("header: got %+v", sale)
	}
	soda, queso := sale.Items[0], sale.Items[1]
	if !soda.UnitPrice.Equal(d("1000")) || !soda.TaxRate.Equal(d("21")) || !soda.UnitCost.Equal(d("600")) ||
		soda.ProductName != "SODA TEST SALES" || len(soda.Batches) != 0 {
		t.Errorf("soda line: got %+v", soda)
	}
	// Lote A 1 × 3000 + lote B 1,5 × 3200 + 0,2 sin lote × 3100 = 8420;
	// 8420 / 2,7 = 3118,52.
	if !queso.UnitPrice.Equal(d("5000")) || !queso.UnitCost.Equal(d("3118.52")) || queso.SaleType != model.SaleTypeWeight ||
		len(queso.Batches) != 2 || queso.Batches[0].BatchID != f.batchA || !queso.Batches[0].Quantity.Equal(d("1")) ||
		queso.Batches[1].BatchID != f.batchB || !queso.Batches[1].Quantity.Equal(d("1.5")) {
		t.Errorf("queso line: got %+v (batches %+v)", queso, queso.Batches)
	}

	// Vuelto: se pagaron 16000; el efectivo queda en 10500.
	if !sale.Change.Equal(d("500")) || !sale.Payments[1].Amount.Equal(d("10500")) ||
		!sale.Payments[1].Tendered.Equal(d("11000")) || sale.Payments[0].Reference != "op 9" {
		t.Errorf("payments: change %s, %+v", sale.Change, sale.Payments)
	}
	var paid decimal.Decimal
	f.scan(t, &paid, `SELECT SUM(amount) FROM payments WHERE sale_id = $1;`, sale.ID)
	if !paid.Equal(sale.Total) {
		t.Errorf("payments in DB: %s, want %s", paid, sale.Total)
	}

	// Stock y lotes.
	if got := f.onHand(t, f.soda); !got.Equal(d("8")) {
		t.Errorf("soda stock: %s, want 8", got)
	}
	if got := f.onHand(t, f.queso); !got.Equal(d("0.3")) {
		t.Errorf("queso stock: %s, want 0.3", got)
	}
	if n := f.count(t, `SELECT count(*) FROM product_batches WHERE product_id = $1 AND remaining_quantity = 0
		AND is_consumed;`, f.queso); n != 2 {
		t.Errorf("consumed batches: %d, want 2", n)
	}

	// Movimientos: soda uno sin lote al promedio; queso uno por lote y uno
	// sin lote al promedio. Todos con la venta.
	rows, err := f.tx.QueryContext(f.ctx, `
		SELECT product_id, batch_id, quantity, unit_cost FROM stock_movements
		WHERE sale_id = $1 AND movement_type = 'SALE' ORDER BY id;`, sale.ID)
	if err != nil {
		t.Fatal(err)
	}
	type mv struct {
		product int64
		batch   *int64
		qty     string
		cost    string
	}
	want := []mv{{f.soda, nil, "-2", "600"}, {f.queso, &f.batchA, "-1", "3000"},
		{f.queso, &f.batchB, "-1.5", "3200"}, {f.queso, nil, "-0.2", "3100"}}
	var got []mv
	for rows.Next() {
		var m mv
		var q, c decimal.Decimal
		if err := rows.Scan(&m.product, &m.batch, &q, &c); err != nil {
			t.Fatal(err)
		}
		m.qty, m.cost = q.String(), c.String()
		got = append(got, m)
	}
	rows.Close()
	if len(got) != len(want) {
		t.Fatalf("movements: got %d, want %d", len(got), len(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.product != w.product || (g.batch == nil) != (w.batch == nil) || (g.batch != nil && *g.batch != *w.batch) ||
			g.qty != w.qty || g.cost != w.cost {
			t.Errorf("movement %d: got %+v, want %+v", i, g, w)
		}
	}
	// Cada asignación a un lote tiene su movimiento con el mismo lote.
	if n := f.count(t, `SELECT count(*) FROM sale_item_batches sib JOIN sale_items si ON si.id = sib.sale_item_id
		WHERE si.sale_id = $1 AND NOT EXISTS (SELECT 1 FROM stock_movements m WHERE m.sale_id = si.sale_id
		AND m.batch_id = sib.batch_id AND m.quantity = -sib.quantity);`, sale.ID); n != 0 {
		t.Errorf("allocations without movement: %d", n)
	}

	// La siguiente venta es la 2, y el queso que queda sale sin lote.
	next, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{Items: []*model.SaleItem{line(f.queso, "0.300")},
		Payments: []*model.Payment{pay(model.PaymentDebit, "1500")}})
	if err != nil {
		t.Fatalf("second checkout: %v", err)
	}
	if next.SaleNumber != 2 || len(next.Items[0].Batches) != 0 || !next.Items[0].UnitCost.Equal(d("3100")) {
		t.Errorf("second sale: got %+v", next)
	}
	if got := f.onHand(t, f.queso); !got.IsZero() {
		t.Errorf("queso stock: %s, want 0", got)
	}

	if got, err := f.svc.Get(f.ctx, sale.ID); err != nil || len(got.Items) != 2 || got.RegisterName != "Caja test" {
		t.Errorf("get: got %+v, %v", got, err)
	}
	if _, err := f.svc.Get(f.ctx, -1); !errors.Is(err, service.ErrInvalidID) {
		t.Errorf("get invalid id: %v", err)
	}
}

func TestCheckoutRejected(t *testing.T) {
	f := newFixture(t)
	cash := func(amount string) []*model.Payment { return []*model.Payment{pay(model.PaymentCash, amount)} }

	tests := []struct {
		name      string
		items     []*model.SaleItem
		payments  []*model.Payment
		wantErr   error
		kind      error // service.ErrValidation o service.ErrConflict
		product   int64 // si es un *LineError, de qué producto
		available string
	}{
		{"no lines", nil, cash("1"), model.ErrSaleNoItems, service.ErrValidation, 0, ""},
		{"not enough stock", []*model.SaleItem{line(f.soda, "11")}, cash("11000"),
			ErrNotEnoughStock, service.ErrConflict, f.soda, "10"},
		{"fraction of a unit", []*model.SaleItem{line(f.soda, "1.5")}, cash("2000"),
			model.ErrSaleQuantityNotWhole, service.ErrValidation, f.soda, ""},
		{"too heavy", []*model.SaleItem{line(f.queso, "50.5")}, cash("300000"),
			model.ErrSaleWeightTooLarge, service.ErrValidation, f.queso, ""},
		{"no price", []*model.SaleItem{line(f.soda, "1"), line(f.noPrice, "1")}, cash("2000"),
			ErrNoPrice, service.ErrConflict, f.noPrice, ""},
		{"inactive", []*model.SaleItem{line(f.inactive, "1")}, cash("100"),
			ErrProductInactive, service.ErrConflict, f.inactive, ""},
		{"combo without components", []*model.SaleItem{line(f.composite, "1")}, cash("100"),
			ErrComboEmpty, service.ErrConflict, f.composite, ""},
		{"unknown product", []*model.SaleItem{line(999999999, "1")}, cash("100"),
			ErrUnknownProduct, service.ErrValidation, 999999999, ""},
		{"4 decimals", []*model.SaleItem{line(f.queso, "0.0001")}, cash("1"),
			model.ErrInvalidSaleQuantity, service.ErrValidation, 0, ""},
		{"payments short", []*model.SaleItem{line(f.soda, "1")}, cash("500"),
			model.ErrPaymentsShort, service.ErrValidation, 0, ""},
		{"card over the total", []*model.SaleItem{line(f.soda, "1")},
			[]*model.Payment{pay(model.PaymentDebit, "2000")}, model.ErrPaymentsOverTotal, service.ErrValidation, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{Items: tt.items, Payments: tt.payments})
			if !errors.Is(err, tt.wantErr) || !errors.Is(err, tt.kind) {
				t.Fatalf("got %v, want %v (%v)", err, tt.wantErr, tt.kind)
			}
			var le *LineError
			if errors.As(err, &le) != (tt.product != 0) {
				t.Fatalf("line error: got %v, want product %d", err, tt.product)
			}
			if le != nil && (le.ProductID != tt.product || (tt.available != "" && !le.Available.Equal(d(tt.available)))) {
				t.Errorf("line error: got %+v", le)
			}
		})
	}

	// Nada quedó guardado: ni ventas, ni stock movido, ni lotes tocados.
	if n := f.count(t, `SELECT count(*) FROM sales WHERE cashier_session_id = $1;`, f.sessionID); n != 0 {
		t.Errorf("sales saved: %d", n)
	}
	if got := f.onHand(t, f.soda); !got.Equal(d("10")) {
		t.Errorf("soda stock: %s, want 10", got)
	}
	if n := f.count(t, `SELECT count(*) FROM product_batches WHERE product_id = $1 AND remaining_quantity < original_quantity;`,
		f.queso); n != 0 {
		t.Errorf("batches touched: %d", n)
	}

	// Caja cerrada: no se vende.
	f.exec(t, `UPDATE cashier_sessions SET status = 'CLOSED', closed_at = now() WHERE id = $1;`, f.sessionID)
	_, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{Items: []*model.SaleItem{line(f.soda, "1")}, Payments: cash("1000")})
	if !errors.Is(err, ErrRegisterClosed) {
		t.Errorf("closed register: got %v, want %v", err, ErrRegisterClosed)
	}
}

// Sin caja en el local: la caja está "cerrada".
func TestCheckoutNoRegister(t *testing.T) {
	f := newFixture(t)
	f.exec(t, `UPDATE cash_registers SET is_active = FALSE WHERE id = $1;`, f.registerID)
	_, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{Items: []*model.SaleItem{line(f.soda, "1")},
		Payments: []*model.Payment{pay(model.PaymentCash, "1000")}})
	if !errors.Is(err, ErrRegisterClosed) || !errors.Is(err, service.ErrConflict) {
		t.Errorf("got %v, want %v", err, ErrRegisterClosed)
	}
}

func TestSearch(t *testing.T) {
	f := newFixture(t)
	got, err := f.svc.Search(f.ctx, "TEST SALES")
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Option{}
	for _, o := range got {
		byName[o.Name] = o
	}
	if _, ok := byName["COMBO TEST SALES"]; !ok {
		t.Error("combos are sold too (0023)")
	}
	if _, ok := byName["INACTIVO TEST SALES"]; ok {
		t.Error("inactive products are not sold")
	}
	if q := byName["QUESO TEST SALES"]; q.Price == nil || !q.Price.Equal(d("5000")) || !q.OnHand.Equal(d("3")) ||
		q.SaleType != model.SaleTypeWeight {
		t.Errorf("queso: got %+v", q)
	}
	if o, ok := byName["SIN PRECIO TEST SALES"]; !ok || o.Price != nil || !o.OnHand.Equal(d("5")) {
		t.Errorf("no price: got %+v (found %v)", o, ok)
	}
	if len(got) != 4 {
		t.Errorf("got %d options, want 4", len(got))
	}
}
