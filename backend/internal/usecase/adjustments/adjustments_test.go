package adjustments

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

// fixture: una transacción que se deshace al terminar, con un solo local
// activo, una categoría y tres productos: uno común, uno con lotes (por
// peso) y un combo.
type fixture struct {
	ctx        context.Context
	tx         *sql.Tx
	userID     int64 // quién hace las cosas (F-13)
	svc        *Service
	categoryID int64
	storeID    int64
	plain      int64
	batched    int64
	composite  int64
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
	f.scan(t, &f.storeID, `INSERT INTO stores (name) VALUES ('Local test adjustments') RETURNING id;`)
	var taxID int64
	f.scan(t, &taxID, `INSERT INTO taxes (name, rate) VALUES ('IVA test adjustments', 21) RETURNING id;`)
	f.scan(t, &f.categoryID, `INSERT INTO categories (name, tax_id) VALUES ('Categoria test adjustments', $1) RETURNING id;`, taxID)
	f.plain = f.newProduct(t, "GASEOSA TEST ADJ", `'UNIT', 'UNIT', FALSE, 30, TRUE`)
	f.batched = f.newProduct(t, "QUESO TEST ADJ", `'WEIGHT', 'KILOGRAM', TRUE, 30, TRUE`)
	f.composite = f.newProduct(t, "PACK TEST ADJ", `'COMPOSITE', 'UNIT', FALSE, 30, TRUE`)
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

func (f *fixture) newProduct(t *testing.T, name, values string) int64 {
	t.Helper()
	var id int64
	f.scan(t, &id, `INSERT INTO products (name, category_id, sale_type, measure_unit, controls_batches,
		margin_percent, is_active) VALUES ($1, $2, `+values+`) RETURNING id;`, name, f.categoryID)
	return id
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func (f *fixture) addStock(t *testing.T, productID int64, qty string) {
	t.Helper()
	if _, err := postgres.NewStockBalanceStore(f.tx).Add(f.ctx, productID, f.storeID, dec(qty)); err != nil {
		t.Fatalf("add stock: %v", err)
	}
}

func (f *fixture) setCost(t *testing.T, productID int64, cost string) {
	t.Helper()
	if _, err := postgres.NewCostStore(f.tx).Create(f.ctx, &model.Cost{ProductID: productID,
		UnitCost: dec(cost), AvgCost: dec(cost)}); err != nil {
		t.Fatalf("set cost: %v", err)
	}
}

func (f *fixture) newBatch(t *testing.T, number, expiry, qty, cost string) int64 {
	t.Helper()
	b, err := postgres.NewBatchStore(f.tx).Create(f.ctx, &model.ProductBatch{ProductID: f.batched, BatchNumber: number,
		ExpiryDate: expiry, OriginalQuantity: dec(qty), UnitCost: dec(cost)})
	if err != nil {
		t.Fatal(err)
	}
	return b.ID
}

func (f *fixture) onHand(t *testing.T, productID int64) decimal.Decimal {
	t.Helper()
	qty, err := postgres.NewStockBalanceStore(f.tx).OnHandAt(f.ctx, productID, f.storeID)
	if err != nil {
		t.Fatal(err)
	}
	return qty
}

func (f *fixture) record(t *testing.T, a *model.StockAdjustment) *Result {
	t.Helper()
	res, err := f.svc.Record(f.ctx, f.userID, a)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	id := res.Adjustment.ID
	usecasetest.WantAuthor(t, f.ctx, f.tx, "exit by", usecasetest.Author("stock_adjustments", "created_by", "id = $2"), f.userID, id)
	usecasetest.WantAuthor(t, f.ctx, f.tx, "exit movements", usecasetest.Author("stock_movements", "performed_by", "adjustment_id = $2"), f.userID, id)
	usecasetest.WantAuthor(t, f.ctx, f.tx, "exit audit", usecasetest.Author("audit_log", "user_id",
		"table_name = 'stock_adjustments' AND record_id = $2"), f.userID, id)
	return res
}

type movement struct{ batch, qty, unitCost, reason string }

func (f *fixture) movements(t *testing.T, adjustmentID int64) []movement {
	t.Helper()
	rows, err := f.tx.QueryContext(f.ctx, `
		SELECT COALESCE(b.batch_number, ''), m.quantity, m.unit_cost, m.reason
		FROM stock_movements m LEFT JOIN product_batches b ON b.id = m.batch_id
		WHERE m.adjustment_id = $1 AND m.movement_type = 'ADJUSTMENT' AND m.store_id = $2
		ORDER BY m.id;`, adjustmentID, f.storeID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []movement
	for rows.Next() {
		var m movement
		var qty, cost decimal.Decimal
		if err := rows.Scan(&m.batch, &qty, &cost, &m.reason); err != nil {
			t.Fatal(err)
		}
		m.qty, m.unitCost = qty.String(), cost.String()
		got = append(got, m)
	}
	return got
}

func checkMovements(t *testing.T, got, want []movement) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got movements %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("movement %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestRecord(t *testing.T) {
	f := newFixture(t)
	f.addStock(t, f.plain, "10")
	f.setCost(t, f.plain, "100")

	in := &model.StockAdjustment{ProductID: f.plain, Reason: model.ReasonOwnConsumption, Quantity: dec("2"), Notes: " mate "}
	res := f.record(t, in)
	a := res.Adjustment
	if a.ID == 0 || a.StoreID != f.storeID || a.Notes != "mate" || a.ProductName != "GASEOSA TEST ADJ" || res.InOpenCount {
		t.Errorf("result: got %+v, in open count %v", a, res.InOpenCount)
	}
	if in.Notes != " mate " {
		t.Errorf("caller's adjustment changed: %q", in.Notes)
	}
	checkMovements(t, f.movements(t, a.ID), []movement{{"", "-2", "100", "OWN_CONSUMPTION"}})
	if got := f.onHand(t, f.plain); !got.Equal(dec("8")) {
		t.Errorf("balance: got %s, want 8", got)
	}
	var costs, audits int
	f.scan(t, &costs, `SELECT count(*) FROM product_costs WHERE product_id = $1;`, f.plain)
	f.scan(t, &audits, `SELECT count(*) FROM audit_log WHERE table_name = 'stock_adjustments' AND record_id = $1;`, a.ID)
	if costs != 1 || audits != 1 {
		t.Errorf("costs %d (average must not change), audits %d", costs, audits)
	}

	// Sin costo nunca: el movimiento va a 0.
	f.addStock(t, f.batched, "1")
	res = f.record(t, &model.StockAdjustment{ProductID: f.batched, Reason: model.ReasonDamage, Quantity: dec("0.4")})
	checkMovements(t, f.movements(t, res.Adjustment.ID), []movement{{"", "-0.4", "0", "DAMAGE"}})

	if recent, err := f.svc.Recent(f.ctx, 10); err != nil || len(recent) != 2 || recent[0].ProductName != "QUESO TEST ADJ" {
		t.Errorf("recent: got %+v, %v", recent, err)
	}
}

func TestRecordBatches(t *testing.T) {
	t.Run("FIFO without a choice", func(t *testing.T) {
		f := newFixture(t)
		f.newBatch(t, "L-2", "2027-02-01", "2", "70")
		f.newBatch(t, "L-1", "2027-01-01", "1", "60")
		f.addStock(t, f.batched, "3")
		res := f.record(t, &model.StockAdjustment{ProductID: f.batched, Reason: model.ReasonExpiration, Quantity: dec("2")})
		checkMovements(t, f.movements(t, res.Adjustment.ID),
			[]movement{{"L-1", "-1", "60", "EXPIRATION"}, {"L-2", "-1", "70", "EXPIRATION"}})
		if batches, _ := f.svc.Batches(f.ctx, f.batched); len(batches) != 1 || !batches[0].RemainingQuantity.Equal(dec("1")) {
			t.Errorf("batches left: got %+v", batches)
		}
	})

	t.Run("chosen batch first, then FIFO", func(t *testing.T) {
		f := newFixture(t)
		later := f.newBatch(t, "L-2", "2027-02-01", "2", "70")
		f.newBatch(t, "L-1", "2027-01-01", "1", "60")
		f.addStock(t, f.batched, "3")
		res := f.record(t, &model.StockAdjustment{ProductID: f.batched, Reason: model.ReasonExpiration,
			Quantity: dec("3"), BatchID: &later})
		checkMovements(t, f.movements(t, res.Adjustment.ID),
			[]movement{{"L-2", "-2", "70", "EXPIRATION"}, {"L-1", "-1", "60", "EXPIRATION"}})
	})

	t.Run("more than the batches: the rest without batch at the average", func(t *testing.T) {
		f := newFixture(t)
		f.newBatch(t, "L-1", "", "1", "60")
		f.addStock(t, f.batched, "3")
		f.setCost(t, f.batched, "90")
		res := f.record(t, &model.StockAdjustment{ProductID: f.batched, Reason: model.ReasonDamage, Quantity: dec("2")})
		checkMovements(t, f.movements(t, res.Adjustment.ID),
			[]movement{{"L-1", "-1", "60", "DAMAGE"}, {"", "-1", "90", "DAMAGE"}})
		if got := f.onHand(t, f.batched); !got.Equal(dec("1")) {
			t.Errorf("balance: got %s, want 1", got)
		}
	})
}

func TestRecordErrors(t *testing.T) {
	f := newFixture(t)
	f.addStock(t, f.plain, "2")
	inactive := f.newProduct(t, "INACTIVO TEST ADJ", `'UNIT', 'UNIT', FALSE, 30, FALSE`)
	otherBatched := f.newProduct(t, "SALAME TEST ADJ", `'WEIGHT', 'KILOGRAM', TRUE, 30, TRUE`)
	f.newBatch(t, "L-1", "", "5", "60")
	f.addStock(t, f.batched, "5")
	foreign, err := postgres.NewBatchStore(f.tx).Create(f.ctx, &model.ProductBatch{ProductID: otherBatched,
		BatchNumber: "OTRO", OriginalQuantity: dec("1"), UnitCost: dec("1")})
	if err != nil {
		t.Fatal(err)
	}
	batch := foreign.ID

	tests := []struct {
		name    string
		in      *model.StockAdjustment
		wantErr error
	}{
		{"nil", nil, service.ErrValidation},
		{"other without notes", &model.StockAdjustment{ProductID: f.plain, Reason: model.ReasonOther, Quantity: dec("1")}, model.ErrAdjustmentNotesRequired},
		{"more than there is", &model.StockAdjustment{ProductID: f.plain, Reason: model.ReasonDamage, Quantity: dec("3")}, service.ErrConflict},
		{"batch on a product without batches", &model.StockAdjustment{ProductID: f.plain, Reason: model.ReasonDamage, Quantity: dec("1"), BatchID: &batch}, ErrBatchNotAllowed},
		{"another product's batch", &model.StockAdjustment{ProductID: f.batched, Reason: model.ReasonDamage, Quantity: dec("1"), BatchID: &batch}, ErrUnknownBatch},
		{"inactive", &model.StockAdjustment{ProductID: inactive, Reason: model.ReasonDamage, Quantity: dec("1")}, ErrProductInactive},
		{"composite", &model.StockAdjustment{ProductID: f.composite, Reason: model.ReasonDamage, Quantity: dec("1")}, ErrCompositeProduct},
		{"unknown", &model.StockAdjustment{ProductID: 999999999, Reason: model.ReasonDamage, Quantity: dec("1")}, ErrUnknownProduct},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := f.svc.Record(f.ctx, f.userID, tt.in); !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}

	_, err = f.svc.Record(f.ctx, f.userID, &model.StockAdjustment{ProductID: f.plain, Reason: model.ReasonDamage, Quantity: dec("3")})
	var short *NotEnoughStockError
	if !errors.As(err, &short) || !short.Available.Equal(dec("2")) {
		t.Errorf("not enough stock: got %v, want available 2", err)
	}

	// Nada de lo que falló dejó rastro.
	var adjustments int
	f.scan(t, &adjustments, `SELECT count(*) FROM stock_adjustments WHERE store_id = $1;`, f.storeID)
	if adjustments != 0 || !f.onHand(t, f.plain).Equal(dec("2")) || !f.onHand(t, f.batched).Equal(dec("5")) {
		t.Errorf("failed exits left %d adjustments, balances %s / %s", adjustments, f.onHand(t, f.plain), f.onHand(t, f.batched))
	}

	f.exec(t, `UPDATE stores SET is_active = FALSE;`)
	if _, err := f.svc.Record(f.ctx, f.userID, &model.StockAdjustment{ProductID: f.plain, Reason: model.ReasonDamage,
		Quantity: dec("1")}); !errors.Is(err, service.ErrConflict) {
		t.Errorf("no active store: got %v, want %v", err, service.ErrConflict)
	}
}

// Un producto ya contado en el conteo abierto: se anota igual y se avisa.
func TestRecordInOpenCount(t *testing.T) {
	f := newFixture(t)
	f.addStock(t, f.plain, "10")
	if in, err := f.svc.InOpenCount(f.ctx, f.plain); err != nil || in {
		t.Errorf("without a count: got %v, %v", in, err)
	}
	counts := postgres.NewStockCountStore(f.tx)
	c, err := counts.CreateDraft(f.ctx, f.storeID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if in, _ := f.svc.InOpenCount(f.ctx, f.plain); in {
		t.Error("count open but product not counted: got true")
	}
	if _, err := counts.CreateItem(f.ctx, c.ID, f.plain, dec("10")); err != nil {
		t.Fatal(err)
	}
	if in, err := f.svc.InOpenCount(f.ctx, f.plain); err != nil || !in {
		t.Errorf("counted: got %v, %v", in, err)
	}
	res := f.record(t, &model.StockAdjustment{ProductID: f.plain, Reason: model.ReasonDamage, Quantity: dec("1")})
	if !res.InOpenCount {
		t.Error("record of a counted product: InOpenCount should be true")
	}
}

type failingAudit struct{}

func (failingAudit) Create(context.Context, postgres.AuditEntry) (int64, error) {
	return 0, errors.New("audit down")
}

// Si algo falla al final (la auditoría), no queda nada de la salida.
func TestRecordRollsBack(t *testing.T) {
	f := newFixture(t)
	f.newBatch(t, "L-1", "", "2", "60")
	f.addStock(t, f.batched, "2")
	f.svc.audit = func(postgres.DBTX) auditStore { return failingAudit{} }

	if _, err := f.svc.Record(f.ctx, f.userID, &model.StockAdjustment{ProductID: f.batched, Reason: model.ReasonExpiration,
		Quantity: dec("1")}); err == nil {
		t.Fatal("record with failing audit: got no error")
	}
	var adjustments, movements int
	f.scan(t, &adjustments, `SELECT count(*) FROM stock_adjustments WHERE store_id = $1;`, f.storeID)
	f.scan(t, &movements, `SELECT count(*) FROM stock_movements WHERE store_id = $1;`, f.storeID)
	batches, _ := f.svc.Batches(f.ctx, f.batched)
	if adjustments != 0 || movements != 0 || !f.onHand(t, f.batched).Equal(dec("2")) ||
		len(batches) != 1 || !batches[0].RemainingQuantity.Equal(dec("2")) {
		t.Errorf("left: %d adjustments, %d movements, balance %s, batches %+v",
			adjustments, movements, f.onHand(t, f.batched), batches)
	}
}
