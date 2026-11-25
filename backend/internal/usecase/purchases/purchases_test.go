package purchases

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"
	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// fixture: una transacción que se deshace al terminar, con un solo local
// activo (los demás se apagan dentro de la transacción), un proveedor, una
// categoría con IVA 21% y dos productos con margen 30%: uno común y uno que
// controla lotes (por peso).
type fixture struct {
	ctx        context.Context
	tx         *sql.Tx
	svc        *Service
	categoryID int64
	storeID    int64
	supplierID int64
	plain      int64 // sin lotes
	batched    int64 // con lotes, por kg
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
	f.exec(t, `UPDATE stores SET is_active = FALSE;`)
	f.scan(t, &f.storeID, `INSERT INTO stores (name) VALUES ('Local test purchases') RETURNING id;`)
	f.scan(t, &f.supplierID, `INSERT INTO suppliers (name) VALUES ('Proveedor test purchases') RETURNING id;`)
	var taxID int64
	f.scan(t, &taxID, `INSERT INTO taxes (name, rate) VALUES ('IVA test purchases', 21) RETURNING id;`)
	f.scan(t, &f.categoryID, `INSERT INTO categories (name, tax_id) VALUES ('Categoria test purchases', $1) RETURNING id;`, taxID)
	f.plain = f.newProduct(t, "GASEOSA TEST PURCHASES", `'UNIT', 'UNIT', FALSE, 30, TRUE`)
	f.batched = f.newProduct(t, "QUESO TEST PURCHASES", `'WEIGHT', 'KILOGRAM', TRUE, 30, TRUE`)
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

// newProduct crea un producto; values son sale_type, measure_unit,
// controls_batches, margin_percent e is_active, en SQL.
func (f *fixture) newProduct(t *testing.T, name, values string) int64 {
	t.Helper()
	var id int64
	f.scan(t, &id, `INSERT INTO products (name, category_id, sale_type, measure_unit, controls_batches,
		margin_percent, is_active) VALUES ($1, $2, `+values+`) RETURNING id;`, name, f.categoryID)
	return id
}

func (f *fixture) count(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	var n int64
	f.scan(t, &n, q, args...)
	return n
}

func (f *fixture) balance(t *testing.T, productID int64) decimal.Decimal {
	t.Helper()
	var qty decimal.Decimal
	f.scan(t, &qty, `SELECT COALESCE(SUM(quantity), 0) FROM stock_balances WHERE product_id = $1;`, productID)
	return qty
}

func (f *fixture) currentCost(t *testing.T, productID int64) *model.Cost {
	t.Helper()
	c, err := postgres.NewCostStore(f.tx).Current(f.ctx, productID)
	if err != nil {
		t.Fatalf("current cost: %v", err)
	}
	return c
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func line(productID int64, qty, cost string) *model.PurchaseItem {
	return &model.PurchaseItem{ProductID: productID, Quantity: dec(qty), UnitCost: dec(cost)}
}

func batchLine(productID int64, qty, cost, batch, expiry string) *model.PurchaseItem {
	it := line(productID, qty, cost)
	it.BatchNumber, it.ExpiryDate = batch, expiry
	return it
}

func (f *fixture) receive(t *testing.T, total string, items ...*model.PurchaseItem) *Result {
	t.Helper()
	res, err := f.svc.Receive(f.ctx, &model.Purchase{SupplierID: f.supplierID, VoucherNumber: "A-0001-00000001",
		Total: dec(total), Items: items})
	if err != nil {
		t.Fatalf("receive: %v", err)
	}
	return res
}

func TestReceiveSavesEverything(t *testing.T) {
	f := newFixture(t)

	// 10 × 100 + 2,5 kg × 60 = 1150.
	res := f.receive(t, "1150",
		line(f.plain, "10", "100"),
		batchLine(f.batched, "2.5", "60", "L-2475", "2027-03-31"))
	p := res.Purchase

	if p.ID == 0 || p.Status != "RECEIVED" || p.ReceivedBy != nil || len(p.Items) != 2 {
		t.Fatalf("purchase: got %+v", p)
	}
	if !res.TotalDifference.IsZero() {
		t.Errorf("total difference: got %s, want 0", res.TotalDifference)
	}

	// Renglones: el común sin lote, el otro con su lote.
	if p.Items[0].BatchID != nil {
		t.Errorf("plain line has a batch: %+v", p.Items[0])
	}
	if p.Items[1].BatchID == nil {
		t.Fatalf("batched line has no batch: %+v", p.Items[1])
	}
	var number, expiry string
	var remaining decimal.Decimal
	f.scan(t, &number, `SELECT batch_number FROM product_batches WHERE id = $1;`, *p.Items[1].BatchID)
	f.scan(t, &expiry, `SELECT to_char(expiry_date, 'YYYY-MM-DD') FROM product_batches WHERE id = $1;`, *p.Items[1].BatchID)
	f.scan(t, &remaining, `SELECT remaining_quantity FROM product_batches WHERE id = $1;`, *p.Items[1].BatchID)
	if number != "L-2475" || expiry != "2027-03-31" || !remaining.Equal(dec("2.5")) {
		t.Errorf("batch: got %q %q %s", number, expiry, remaining)
	}

	// Movimientos, saldos y costos.
	if n := f.count(t, `SELECT count(*) FROM stock_movements WHERE purchase_id = $1 AND movement_type = 'PURCHASE'
		AND performed_by IS NULL;`, p.ID); n != 2 {
		t.Errorf("movements: got %d, want 2", n)
	}
	if got := f.balance(t, f.plain); !got.Equal(dec("10")) {
		t.Errorf("plain balance: got %s, want 10", got)
	}
	if got := f.balance(t, f.batched); !got.Equal(dec("2.5")) {
		t.Errorf("batched balance: got %s, want 2.5", got)
	}
	if c := f.currentCost(t, f.plain); !c.UnitCost.Equal(dec("100")) || !c.AvgCost.Equal(dec("100")) {
		t.Errorf("plain cost: got %+v", c)
	}

	// Auditoría: una fila con la compra y sus renglones.
	var lines int64
	f.scan(t, &lines, `SELECT jsonb_array_length(new_data->'items') FROM audit_log
		WHERE table_name = 'purchases' AND record_id = $1;`, p.ID)
	if lines != 2 {
		t.Errorf("audit lines: got %d, want 2", lines)
	}

	// Sugerencias: margen 30%, IVA 21%. 100 × 1,21 / 0,7 = 172,857… → 172,86.
	if len(res.Suggestions) != 2 {
		t.Fatalf("suggestions: got %+v", res.Suggestions)
	}
	s := res.Suggestions[0]
	if s.ProductID != f.plain || s.ProductName != "GASEOSA TEST PURCHASES" || s.SuggestedPrice == nil ||
		!s.SuggestedPrice.Equal(dec("172.86")) || s.CurrentPrice != nil {
		t.Errorf("suggestion: got %+v", s)
	}
}

func TestReceiveAveragesWithStock(t *testing.T) {
	f := newFixture(t)
	f.receive(t, "1000", line(f.plain, "10", "100"))

	res := f.receive(t, "1300", line(f.plain, "10", "130"))
	// (100 × 10 + 130 × 10) / 20 = 115.
	if s := res.Suggestions[0]; !s.UnitCost.Equal(dec("130")) || !s.AvgCost.Equal(dec("115")) {
		t.Errorf("suggestion: got unit %s avg %s, want 130 and 115", s.UnitCost, s.AvgCost)
	}
	if got := f.balance(t, f.plain); !got.Equal(dec("20")) {
		t.Errorf("balance: got %s, want 20", got)
	}
}

// El mismo producto en dos renglones: el segundo promedia contra lo que dejó
// el primero. Una sugerencia por producto, con el último costo.
func TestReceiveSameProductTwice(t *testing.T) {
	f := newFixture(t)

	res := f.receive(t, "3000", line(f.plain, "10", "100"), line(f.plain, "10", "200"))
	if len(res.Suggestions) != 1 {
		t.Fatalf("suggestions: got %+v, want one per product", res.Suggestions)
	}
	if s := res.Suggestions[0]; !s.UnitCost.Equal(dec("200")) || !s.AvgCost.Equal(dec("150")) {
		t.Errorf("suggestion: got unit %s avg %s, want 200 and 150", s.UnitCost, s.AvgCost)
	}
	if n := f.count(t, `SELECT count(*) FROM product_costs WHERE product_id = $1;`, f.plain); n != 2 {
		t.Errorf("costs: got %d, want 2 (one per line)", n)
	}
}

func TestReceiveMergesBatches(t *testing.T) {
	f := newFixture(t)

	res := f.receive(t, "0",
		batchLine(f.batched, "2.5", "60", "L-1", "2027-03-31"),
		batchLine(f.batched, "1.25", "60", "L-1", "2027-03-31"), // igual: se junta
		batchLine(f.batched, "1", "70", "L-1", "2027-03-31"),    // otro costo: otro lote
		batchLine(f.batched, "1", "60", "L-1", "2027-04-30"))    // otro vencimiento: otro lote
	items := res.Purchase.Items

	if *items[0].BatchID != *items[1].BatchID {
		t.Errorf("same batch, expiry and cost: got batches %d and %d, want one", *items[0].BatchID, *items[1].BatchID)
	}
	if *items[2].BatchID == *items[0].BatchID || *items[3].BatchID == *items[0].BatchID {
		t.Error("different cost or expiry: merged into the same batch")
	}
	var original, remaining decimal.Decimal
	f.scan(t, &original, `SELECT original_quantity FROM product_batches WHERE id = $1;`, *items[0].BatchID)
	f.scan(t, &remaining, `SELECT remaining_quantity FROM product_batches WHERE id = $1;`, *items[0].BatchID)
	if !original.Equal(dec("3.75")) || !remaining.Equal(dec("3.75")) {
		t.Errorf("merged batch: got original %s remaining %s, want 3.75", original, remaining)
	}
	if n := f.count(t, `SELECT count(*) FROM product_batches WHERE product_id = $1;`, f.batched); n != 3 {
		t.Errorf("batches: got %d, want 3", n)
	}
	// Cada renglón tiene su movimiento, aunque compartan lote.
	if n := f.count(t, `SELECT count(*) FROM stock_movements WHERE purchase_id = $1;`, res.Purchase.ID); n != 4 {
		t.Errorf("movements: got %d, want 4", n)
	}
}

func TestReceiveTotalDifferenceIsAWarning(t *testing.T) {
	f := newFixture(t)
	res := f.receive(t, "1200", line(f.plain, "10", "100"))
	if !res.TotalDifference.Equal(dec("200")) {
		t.Errorf("difference: got %s, want 200", res.TotalDifference)
	}
	if !res.Purchase.Total.Equal(dec("1200")) {
		t.Errorf("saved total: got %s, want what was typed (1200)", res.Purchase.Total)
	}
}

// Los errores no dejan nada: ni compra, ni stock, ni costos.
func TestReceiveErrors(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, f *fixture) *model.Purchase
		wantErr  error
		wantLine string // "line 2: " si es de un renglón
	}{
		{"invalid purchase", func(t *testing.T, f *fixture) *model.Purchase {
			return &model.Purchase{SupplierID: f.supplierID}
		}, model.ErrPurchaseNoItems, ""},
		{"unknown supplier", func(t *testing.T, f *fixture) *model.Purchase {
			return &model.Purchase{SupplierID: 999_999_999, Items: []*model.PurchaseItem{line(f.plain, "1", "1")}}
		}, ErrUnknownSupplier, ""},
		{"inactive supplier", func(t *testing.T, f *fixture) *model.Purchase {
			f.exec(t, `UPDATE suppliers SET is_active = FALSE WHERE id = $1;`, f.supplierID)
			return &model.Purchase{SupplierID: f.supplierID, Items: []*model.PurchaseItem{line(f.plain, "1", "1")}}
		}, ErrSupplierInactive, ""},
		{"no active store", func(t *testing.T, f *fixture) *model.Purchase {
			f.exec(t, `UPDATE stores SET is_active = FALSE;`)
			return &model.Purchase{SupplierID: f.supplierID, Items: []*model.PurchaseItem{line(f.plain, "1", "1")}}
		}, store.ErrNoActiveStore, ""},
		{"several active stores", func(t *testing.T, f *fixture) *model.Purchase {
			f.exec(t, `INSERT INTO stores (name) VALUES ('Otro local test purchases');`)
			return &model.Purchase{SupplierID: f.supplierID, Items: []*model.PurchaseItem{line(f.plain, "1", "1")}}
		}, store.ErrSeveralActiveStores, ""},
		{"unknown product", func(t *testing.T, f *fixture) *model.Purchase {
			return &model.Purchase{SupplierID: f.supplierID,
				Items: []*model.PurchaseItem{line(f.plain, "1", "1"), line(999_999_999, "1", "1")}}
		}, ErrUnknownProduct, "line 2: "},
		{"inactive product", func(t *testing.T, f *fixture) *model.Purchase {
			id := f.newProduct(t, "INACTIVO TEST PURCHASES", `'UNIT', 'UNIT', FALSE, 30, FALSE`)
			return &model.Purchase{SupplierID: f.supplierID,
				Items: []*model.PurchaseItem{line(f.plain, "1", "1"), line(id, "1", "1")}}
		}, ErrProductInactive, "line 2: "},
		{"composite product", func(t *testing.T, f *fixture) *model.Purchase {
			id := f.newProduct(t, "COMBO TEST PURCHASES", `'COMPOSITE', 'UNIT', FALSE, 30, TRUE`)
			return &model.Purchase{SupplierID: f.supplierID, Items: []*model.PurchaseItem{line(id, "1", "1")}}
		}, ErrCompositeProduct, "line 1: "},
		{"batch required", func(t *testing.T, f *fixture) *model.Purchase {
			return &model.Purchase{SupplierID: f.supplierID, Items: []*model.PurchaseItem{line(f.batched, "1", "1")}}
		}, ErrBatchRequired, "line 1: "},
		{"batch not allowed", func(t *testing.T, f *fixture) *model.Purchase {
			return &model.Purchase{SupplierID: f.supplierID,
				Items: []*model.PurchaseItem{batchLine(f.plain, "1", "1", "L-1", "")}}
		}, ErrBatchNotAllowed, "line 1: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			p := tt.setup(t, f)
			before := f.count(t, `SELECT count(*) FROM purchases;`)

			_, err := f.svc.Receive(f.ctx, p)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got %v, want %v", err, tt.wantErr)
			}
			// Todos son de validación o de conflicto: la API responde 400 o 409.
			if !errors.Is(err, service.ErrValidation) && !errors.Is(err, service.ErrConflict) {
				t.Errorf("error %v is neither service.ErrValidation nor service.ErrConflict", err)
			}
			if tt.wantLine != "" && !strings.HasPrefix(err.Error(), tt.wantLine) {
				t.Errorf("error %q does not start with %q", err, tt.wantLine)
			}
			if after := f.count(t, `SELECT count(*) FROM purchases;`); after != before {
				t.Errorf("purchases: %d before, %d after", before, after)
			}
			if got := f.balance(t, f.plain); !got.IsZero() {
				t.Errorf("stock left: %s", got)
			}
		})
	}
}

func TestReceiveDoesNotChangeTheInput(t *testing.T) {
	f := newFixture(t)
	in := &model.Purchase{SupplierID: f.supplierID, VoucherNumber: "  A-1  ", Items: []*model.PurchaseItem{
		batchLine(f.batched, "1", "60", " L-1 ", "")}}

	if _, err := f.svc.Receive(f.ctx, in); err != nil {
		t.Fatal(err)
	}
	if in.ID != 0 || in.VoucherNumber != "  A-1  " || in.Items[0].BatchNumber != " L-1 " ||
		in.Items[0].BatchID != nil || in.Items[0].PurchaseID != 0 {
		t.Errorf("input changed: %+v %+v", in, in.Items[0])
	}
}

// failingAudit simula que el INSERT en audit_log falla al final, con todo lo
// demás ya guardado en la transacción.
type failingAudit struct{}

var errAuditInsert = errors.New("audit insert failed")

func (failingAudit) Create(context.Context, postgres.AuditEntry) (int64, error) {
	return 0, errAuditInsert
}

func TestReceiveRollsBackWhenAuditFails(t *testing.T) {
	f := newFixture(t)
	f.svc.audit = func(postgres.DBTX) auditStore { return failingAudit{} }

	_, err := f.svc.Receive(f.ctx, &model.Purchase{SupplierID: f.supplierID, Items: []*model.PurchaseItem{
		line(f.plain, "10", "100"), batchLine(f.batched, "1", "60", "L-1", "2027-03-31")}})
	if !errors.Is(err, errAuditInsert) {
		t.Fatalf("got %v, want %v", err, errAuditInsert)
	}
	for _, q := range []string{
		`SELECT count(*) FROM purchases WHERE supplier_id = $1;`,
		`SELECT count(*) FROM stock_movements m JOIN purchases p ON p.id = m.purchase_id WHERE p.supplier_id = $1;`,
	} {
		if n := f.count(t, q, f.supplierID); n != 0 {
			t.Errorf("%q: got %d, want 0", q, n)
		}
	}
	for _, id := range []int64{f.plain, f.batched} {
		if got := f.balance(t, id); !got.IsZero() {
			t.Errorf("product %d stock: got %s, want 0", id, got)
		}
		if n := f.count(t, `SELECT count(*) FROM product_costs WHERE product_id = $1;`, id); n != 0 {
			t.Errorf("product %d costs: got %d, want 0", id, n)
		}
		if n := f.count(t, `SELECT count(*) FROM product_batches WHERE product_id = $1;`, id); n != 0 {
			t.Errorf("product %d batches: got %d, want 0", id, n)
		}
	}
}

func TestGet(t *testing.T) {
	f := newFixture(t)
	res := f.receive(t, "1000", line(f.plain, "10", "100"))

	got, err := f.svc.Get(f.ctx, res.Purchase.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != res.Purchase.ID || len(got.Items) != 1 || got.Items[0].ProductID != f.plain {
		t.Errorf("get: got %+v", got)
	}

	if _, err := f.svc.Get(f.ctx, 999_999_999); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("missing: got %v, want %v", err, service.ErrNotFound)
	}
	if _, err := f.svc.Get(f.ctx, 0); !errors.Is(err, service.ErrInvalidID) {
		t.Errorf("id 0: got %v, want %v", err, service.ErrInvalidID)
	}
}
