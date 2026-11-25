package costs

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
)

// fixture: una transacción que se deshace al terminar, un local y un producto
// activo con margen 30%.
type fixture struct {
	ctx        context.Context
	tx         *sql.Tx
	svc        *Service
	categoryID int64
	storeID    int64
	productID  int64
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
	var taxID int64
	f.scan(t, &taxID, `INSERT INTO taxes (name, rate) VALUES ('IVA test costs', 21) RETURNING id;`)
	f.scan(t, &f.categoryID, `INSERT INTO categories (name, tax_id) VALUES ('Categoria test costs', $1) RETURNING id;`, taxID)
	f.scan(t, &f.storeID, `INSERT INTO stores (name) VALUES ('Local test costs') RETURNING id;`)
	f.productID = f.newProduct(t, "PRODUCTO TEST COSTS", "30", true)
	return f
}

func (f *fixture) scan(t *testing.T, dest any, q string, args ...any) {
	t.Helper()
	if err := f.tx.QueryRowContext(f.ctx, q, args...).Scan(dest); err != nil {
		t.Fatalf("fixture %q: %v", q, err)
	}
}

func (f *fixture) newProduct(t *testing.T, name, margin string, active bool) int64 {
	t.Helper()
	var id int64
	f.scan(t, &id, `INSERT INTO products (name, category_id, margin_percent, is_active)
		VALUES ($1, $2, $3, $4) RETURNING id;`, name, f.categoryID, margin, active)
	return id
}

// setStock pone el saldo del producto en el local del fixture.
func (f *fixture) setStock(t *testing.T, productID int64, qty string) {
	t.Helper()
	_, err := f.tx.ExecContext(f.ctx, `
		INSERT INTO stock_balances (product_id, store_id, quantity) VALUES ($1, $2, $3)
		ON CONFLICT (product_id, store_id) DO UPDATE SET quantity = EXCLUDED.quantity;`,
		productID, f.storeID, qty)
	if err != nil {
		t.Fatalf("set stock: %v", err)
	}
}

func (f *fixture) count(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	var n int64
	f.scan(t, &n, q, args...)
	return n
}

func (f *fixture) costsOf(t *testing.T, productID int64) int64 {
	return f.count(t, `SELECT count(*) FROM product_costs WHERE product_id = $1;`, productID)
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error: got %v, want %v", got, want)
	}
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func (f *fixture) set(t *testing.T, productID int64, unitCost string) *Result {
	t.Helper()
	res, err := f.svc.Set(f.ctx, productID, dec(unitCost))
	if err != nil {
		t.Fatalf("set %s: %v", unitCost, err)
	}
	return res
}

func TestSetSavesCostAndAudit(t *testing.T) {
	f := newFixture(t)

	res := f.set(t, f.productID, "700")
	c := res.Cost
	if c.ID == 0 || c.ProductID != f.productID || !c.UnitCost.Equal(dec("700")) {
		t.Errorf("set: got %+v", c)
	}

	var unitCost, avgCost string
	f.scan(t, &unitCost, `SELECT new_data->>'unit_cost' FROM audit_log
		WHERE table_name = 'product_costs' AND record_id = $1;`, c.ID)
	f.scan(t, &avgCost, `SELECT new_data->>'avg_cost' FROM audit_log
		WHERE table_name = 'product_costs' AND record_id = $1;`, c.ID)
	if !dec(unitCost).Equal(dec("700")) || !dec(avgCost).Equal(dec("700")) {
		t.Errorf("audit: got unit %q avg %q, want 700 and 700", unitCost, avgCost)
	}
}

// El promedio en la carga manual (decisión del 2026-11-25): solo se fija si no
// había costo o no hay stock.
func TestSetAverageCost(t *testing.T) {
	tests := []struct {
		name    string
		prev    string // costo previo; "" si no tiene
		stock   string // saldo; "" si no tiene fila
		newCost string
		wantAvg string
	}{
		{name: "first cost, no stock", newCost: "700", wantAvg: "700"},
		{name: "first cost, with stock", stock: "10", newCost: "700", wantAvg: "700"},
		{name: "previous cost, stock 0", prev: "500", stock: "0", newCost: "700", wantAvg: "700"},
		{name: "previous cost, no balance row", prev: "500", newCost: "700", wantAvg: "700"},
		{name: "previous cost, with stock: average kept", prev: "500", stock: "10", newCost: "700", wantAvg: "500"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			if tt.prev != "" {
				f.set(t, f.productID, tt.prev)
			}
			if tt.stock != "" {
				f.setStock(t, f.productID, tt.stock)
			}

			res := f.set(t, f.productID, tt.newCost)
			if !res.Cost.UnitCost.Equal(dec(tt.newCost)) || !res.Cost.AvgCost.Equal(dec(tt.wantAvg)) {
				t.Errorf("got unit %s avg %s, want unit %s avg %s",
					res.Cost.UnitCost, res.Cost.AvgCost, tt.newCost, tt.wantAvg)
			}
		})
	}
}

// El costo se carga sin IVA y el precio de venta lo incluye (decisión del
// 2026-11-25): la sugerencia suma el IVA de la categoría del producto.
func TestSetSuggestsPrice(t *testing.T) {
	f := newFixture(t)

	// Categoría con IVA 21%, margen 30%: 700 / 0,7 = 1000 sin IVA → 1210.
	res := f.set(t, f.productID, "700")
	if res.SuggestedPrice == nil || !res.SuggestedPrice.Equal(dec("1210")) {
		t.Errorf("suggested: got %v, want 1210 (with 21%% IVA)", res.SuggestedPrice)
	}

	// Otra categoría, IVA 10,5%: 1000 sin IVA → 1105.
	var taxID, categoryID, productID int64
	f.scan(t, &taxID, `INSERT INTO taxes (name, rate) VALUES ('IVA 10,5 test costs', 10.5) RETURNING id;`)
	f.scan(t, &categoryID, `INSERT INTO categories (name, tax_id) VALUES ('Categoria 10,5 test costs', $1) RETURNING id;`, taxID)
	f.scan(t, &productID, `INSERT INTO products (name, category_id, margin_percent)
		VALUES ('PRODUCTO 10,5 TEST COSTS', $1, 30) RETURNING id;`, categoryID)
	if res := f.set(t, productID, "700"); res.SuggestedPrice == nil || !res.SuggestedPrice.Equal(dec("1105")) {
		t.Errorf("suggested: got %v, want 1105 (with 10.5%% IVA)", res.SuggestedPrice)
	}
	// La sugerencia no se guarda como precio.
	if n := f.count(t, `SELECT count(*) FROM prices WHERE product_id = $1;`, f.productID); n != 0 {
		t.Errorf("prices saved: %d, want 0", n)
	}

	manual := f.newProduct(t, "SIN MARGEN TEST COSTS", "0", true)
	if res := f.set(t, manual, "700"); res.SuggestedPrice != nil {
		t.Errorf("margin 0: got suggestion %v, want nil", res.SuggestedPrice)
	}
}

func TestSetInvalidCost(t *testing.T) {
	f := newFixture(t)

	_, err := f.svc.Set(f.ctx, f.productID, dec("0"))
	wantErr(t, err, service.ErrValidation)
	wantErr(t, err, model.ErrInvalidCost)
	if n := f.costsOf(t, f.productID); n != 0 {
		t.Errorf("costs saved: %d, want 0", n)
	}
}

func TestSetUnknownProduct(t *testing.T) {
	f := newFixture(t)

	_, err := f.svc.Set(f.ctx, 999_999_999, dec("100"))
	wantErr(t, err, service.ErrNotFound)
}

func TestSetInvalidID(t *testing.T) {
	f := newFixture(t)

	_, err := f.svc.Set(f.ctx, 0, dec("100"))
	wantErr(t, err, service.ErrInvalidID)
}

func TestSetInactiveProduct(t *testing.T) {
	f := newFixture(t)
	inactive := f.newProduct(t, "INACTIVO TEST COSTS", "30", false)

	_, err := f.svc.Set(f.ctx, inactive, dec("100"))
	wantErr(t, err, ErrProductInactive)
	wantErr(t, err, service.ErrConflict)
	if n := f.costsOf(t, inactive); n != 0 {
		t.Errorf("costs saved: %d, want 0", n)
	}
}

// failingAudit simula que el INSERT en audit_log falla cuando el costo ya se
// guardó en la misma transacción.
type failingAudit struct{}

var errAuditInsert = errors.New("audit insert failed")

func (failingAudit) Create(context.Context, postgres.AuditEntry) (int64, error) {
	return 0, errAuditInsert
}

func TestSetRollsBackCostWhenAuditFails(t *testing.T) {
	f := newFixture(t)
	f.svc.audit = func(postgres.DBTX) auditStore { return failingAudit{} }

	_, err := f.svc.Set(f.ctx, f.productID, dec("100"))
	wantErr(t, err, errAuditInsert)
	if n := f.costsOf(t, f.productID); n != 0 {
		t.Errorf("costs left after rollback: %d, want 0", n)
	}
}

func TestHistory(t *testing.T) {
	f := newFixture(t)

	hist, err := f.svc.History(f.ctx, f.productID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if hist == nil || len(hist) != 0 {
		t.Errorf("history: got %#v, want empty non-nil", hist)
	}

	f.set(t, f.productID, "100")
	second := f.set(t, f.productID, "120")
	hist, err = f.svc.History(f.ctx, f.productID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 2 || hist[0].ID != second.Cost.ID {
		t.Errorf("history: got %+v, want 2 costs with the newest first", hist)
	}

	_, err = f.svc.History(f.ctx, 999_999_999)
	wantErr(t, err, service.ErrNotFound)
}
