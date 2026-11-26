package stockcounts

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

func (f *fixture) setCost(t *testing.T, productID int64, cost string) {
	t.Helper()
	if _, err := postgres.NewCostStore(f.tx).Create(f.ctx, &model.Cost{ProductID: productID,
		UnitCost: dec(cost), AvgCost: dec(cost)}); err != nil {
		t.Fatalf("set cost: %v", err)
	}
}

func (f *fixture) cost(t *testing.T, productID int64) *model.Cost {
	t.Helper()
	c, err := postgres.NewCostStore(f.tx).Current(f.ctx, productID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		t.Fatalf("current cost: %v", err)
	}
	return c
}

func (f *fixture) onHand(t *testing.T, productID int64) decimal.Decimal {
	t.Helper()
	qty, err := postgres.NewStockBalanceStore(f.tx).OnHandAt(f.ctx, productID, f.storeID)
	if err != nil {
		t.Fatal(err)
	}
	return qty
}

// movement es un movimiento del conteo, como lo deja la confirmación.
type movement struct {
	batch    string // número de lote, vacío sin lote
	qty      string
	unitCost string
}

func (f *fixture) movements(t *testing.T, countID, productID int64) []movement {
	t.Helper()
	rows, err := f.tx.QueryContext(f.ctx, `
		SELECT COALESCE(b.batch_number, ''), m.quantity, m.unit_cost, m.movement_type, m.reason, m.store_id
		FROM stock_movements m
		LEFT JOIN product_batches b ON b.id = m.batch_id
		WHERE m.count_id = $1 AND m.product_id = $2
		ORDER BY m.id;`, countID, productID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []movement
	for rows.Next() {
		var m movement
		var qty, cost decimal.Decimal
		var typ, reason string
		var storeID int64
		if err := rows.Scan(&m.batch, &qty, &cost, &typ, &reason, &storeID); err != nil {
			t.Fatal(err)
		}
		if typ != "ADJUSTMENT" || reason != "COUNT" || storeID != f.storeID {
			t.Errorf("movement %+v: type %s reason %s store %d", m, typ, reason, storeID)
		}
		m.qty, m.unitCost = qty.String(), cost.String()
		got = append(got, m)
	}
	return got
}

func checkMovements(t *testing.T, name string, got, want []movement) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: got movements %+v, want %+v", name, got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s, movement %d: got %+v, want %+v", name, i, got[i], want[i])
		}
	}
}

func TestConfirm(t *testing.T) {
	f := newFixture(t)
	short := f.plain // faltan 3
	f.addStock(t, short, "10")
	f.setCost(t, short, "100")
	initial := f.newProduct(t, "YERBA TEST COUNTS", `'UNIT', 'UNIT', FALSE, 30, TRUE`) // inventario inicial
	same := f.newProduct(t, "FIDEOS TEST COUNTS", `'UNIT', 'UNIT', FALSE, 30, TRUE`)   // coincide
	f.addStock(t, same, "5")
	untouched := f.newProduct(t, "ARROZ TEST COUNTS", `'UNIT', 'UNIT', FALSE, 30, TRUE`) // no se cuenta
	f.addStock(t, untouched, "8")

	c := f.open(t)
	f.add(t, c.ID, short, "Góndola", "4")
	f.add(t, c.ID, short, "Depósito", "3")
	f.add(t, c.ID, initial, "Depósito", "24")
	cost := dec("50")
	if err := f.svc.SetDetails(f.ctx, c.ID, initial, &cost, ""); err != nil {
		t.Fatal(err)
	}
	f.add(t, c.ID, same, "Góndola", "5")

	res, err := f.svc.Confirm(f.ctx, c.ID)
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if res.Adjusted != 2 || res.Count.Status != model.CountConfirmed || res.Count.ConfirmedAt == nil || len(res.Count.Items) != 3 {
		t.Errorf("result: got adjusted %d, count %+v", res.Adjusted, res.Count)
	}

	checkMovements(t, "short", f.movements(t, c.ID, short), []movement{{"", "-3", "100"}})
	checkMovements(t, "initial", f.movements(t, c.ID, initial), []movement{{"", "24", "50"}})
	checkMovements(t, "same", f.movements(t, c.ID, same), nil)
	for name, want := range map[string]struct {
		id  int64
		qty string
	}{"short": {short, "7"}, "initial": {initial, "24"}, "same": {same, "5"}, "untouched": {untouched, "8"}} {
		if got := f.onHand(t, want.id); !got.Equal(dec(want.qty)) {
			t.Errorf("%s balance: got %s, want %s", name, got, want.qty)
		}
	}
	if got := f.cost(t, initial); got == nil || !got.AvgCost.Equal(dec("50")) {
		t.Errorf("initial cost: got %+v, want avg 50", got)
	}
	if got := f.cost(t, short); !got.AvgCost.Equal(dec("100")) {
		t.Errorf("short cost changed: got %+v", got)
	}

	var audits int
	if err := f.tx.QueryRowContext(f.ctx, `SELECT count(*) FROM audit_log
		WHERE table_name = 'stock_counts' AND record_id = $1;`, c.ID).Scan(&audits); err != nil || audits != 1 {
		t.Errorf("audit rows: %d, %v", audits, err)
	}

	if _, err := f.svc.Confirm(f.ctx, c.ID); !errors.Is(err, service.ErrConflict) {
		t.Errorf("confirm twice: got %v, want %v", err, service.ErrConflict)
	}
	if _, err := f.svc.Confirm(f.ctx, 999999999); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("confirm missing: got %v, want %v", err, service.ErrNotFound)
	}
}

// Lo que sobra sin costo va al promedio y no lo cambia; con costo, el
// promedio se mueve como en una compra.
func TestConfirmCost(t *testing.T) {
	f := newFixture(t)
	noCost := f.plain
	f.addStock(t, noCost, "2")
	f.setCost(t, noCost, "100")
	withCost := f.newProduct(t, "YERBA TEST COUNTS", `'UNIT', 'UNIT', FALSE, 30, TRUE`)
	f.addStock(t, withCost, "10")
	f.setCost(t, withCost, "100")

	c := f.open(t)
	f.add(t, c.ID, noCost, "A", "5")
	f.add(t, c.ID, withCost, "A", "20")
	cost := dec("130")
	if err := f.svc.SetDetails(f.ctx, c.ID, withCost, &cost, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Confirm(f.ctx, c.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	checkMovements(t, "no cost", f.movements(t, c.ID, noCost), []movement{{"", "3", "100"}})
	if got := f.cost(t, noCost); !got.AvgCost.Equal(dec("100")) || !got.UnitCost.Equal(dec("100")) {
		t.Errorf("no cost: got %+v, want 100", got)
	}
	checkMovements(t, "with cost", f.movements(t, c.ID, withCost), []movement{{"", "10", "130"}})
	// (100 × 10 + 130 × 10) / 20 = 115
	if got := f.cost(t, withCost); !got.AvgCost.Equal(dec("115")) || !got.UnitCost.Equal(dec("130")) {
		t.Errorf("with cost: got %+v, want avg 115 unit 130", got)
	}

	// Sin costo nunca: el movimiento va a 0 y no se inventa un costo.
	never := f.newProduct(t, "NUNCA COSTO TEST COUNTS", `'UNIT', 'UNIT', FALSE, 30, TRUE`)
	c2 := f.open(t)
	f.add(t, c2.ID, never, "A", "1")
	if _, err := f.svc.Confirm(f.ctx, c2.ID); err != nil {
		t.Fatalf("confirm 2: %v", err)
	}
	checkMovements(t, "never", f.movements(t, c2.ID, never), []movement{{"", "1", "0"}})
	if got := f.cost(t, never); got != nil {
		t.Errorf("never: got cost %+v, want none", got)
	}
}

func TestConfirmBatches(t *testing.T) {
	f := newFixture(t)
	batches := postgres.NewBatchStore(f.tx)
	newBatch := func(productID int64, number, expiry, qty, cost string) int64 {
		t.Helper()
		b, err := batches.Create(f.ctx, &model.ProductBatch{ProductID: productID, BatchNumber: number,
			ExpiryDate: expiry, OriginalQuantity: dec(qty), UnitCost: dec(cost)})
		if err != nil {
			t.Fatal(err)
		}
		return b.ID
	}
	remaining := func(id int64) string {
		t.Helper()
		var qty decimal.Decimal
		if err := f.tx.QueryRowContext(f.ctx, `SELECT remaining_quantity FROM product_batches WHERE id = $1;`, id).Scan(&qty); err != nil {
			t.Fatal(err)
		}
		return qty.String()
	}

	// Inventario inicial de un producto con lotes: lote I-<conteo>.
	initial := f.batched
	cost := dec("4000")

	// Falta, y los lotes alcanzan: FIFO por vencimiento.
	short := f.newProduct(t, "SALAME TEST COUNTS", `'WEIGHT', 'KILOGRAM', TRUE, 30, TRUE`)
	later := newBatch(short, "L-2", "2027-02-01", "1", "70")
	sooner := newBatch(short, "L-1", "2027-01-01", "1", "60")
	f.addStock(t, short, "2")

	// Falta más de lo que hay en lotes: el resto sin lote, al promedio.
	beyond := f.newProduct(t, "JAMON TEST COUNTS", `'WEIGHT', 'KILOGRAM', TRUE, 30, TRUE`)
	only := newBatch(beyond, "L-9", "", "2", "80")
	f.addStock(t, beyond, "3")
	f.setCost(t, beyond, "90")

	c := f.open(t)
	f.add(t, c.ID, initial, "Heladera", "2.5")
	if err := f.svc.SetDetails(f.ctx, c.ID, initial, &cost, "2027-03-31"); err != nil {
		t.Fatal(err)
	}
	f.add(t, c.ID, short, "Heladera", "0.5")
	f.add(t, c.ID, beyond, "Heladera", "0")
	if _, err := f.svc.Confirm(f.ctx, c.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}

	batchNumber := AutoBatchNumber(c.ID)
	checkMovements(t, "initial", f.movements(t, c.ID, initial), []movement{{batchNumber, "2.5", "4000"}})
	var expiry string
	var left decimal.Decimal
	if err := f.tx.QueryRowContext(f.ctx, `SELECT to_char(expiry_date, 'YYYY-MM-DD'), remaining_quantity
		FROM product_batches WHERE product_id = $1 AND batch_number = $2;`, initial, batchNumber).Scan(&expiry, &left); err != nil {
		t.Fatalf("initial batch: %v", err)
	}
	if expiry != "2027-03-31" || !left.Equal(dec("2.5")) || !strings.HasPrefix(batchNumber, "I-") {
		t.Errorf("initial batch %s: expiry %s remaining %s", batchNumber, expiry, left)
	}

	checkMovements(t, "short", f.movements(t, c.ID, short), []movement{{"L-1", "-1", "60"}, {"L-2", "-0.5", "70"}})
	if remaining(sooner) != "0" || remaining(later) != "0.5" {
		t.Errorf("short batches: sooner %s later %s, want 0 and 0.5", remaining(sooner), remaining(later))
	}

	checkMovements(t, "beyond", f.movements(t, c.ID, beyond), []movement{{"L-9", "-2", "80"}, {"", "-1", "90"}})
	if remaining(only) != "0" || !f.onHand(t, beyond).IsZero() {
		t.Errorf("beyond: batch %s, balance %s; want 0 and 0", remaining(only), f.onHand(t, beyond))
	}
}

// Una compra que entra en medio del conteo no se pierde: se mueve la
// diferencia contra la foto.
func TestConfirmKeepsPurchaseDuringCount(t *testing.T) {
	f := newFixture(t)
	f.addStock(t, f.plain, "10")
	c := f.open(t)
	f.add(t, c.ID, f.plain, "A", "7")
	f.addStock(t, f.plain, "5")

	if _, err := f.svc.Confirm(f.ctx, c.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if got := f.onHand(t, f.plain); !got.Equal(dec("12")) {
		t.Errorf("balance: got %s, want 12 (7 counted + 5 received)", got)
	}
}

// Si el stock bajó por otro lado después de la foto y el ajuste lo dejaría
// negativo, no se confirma nada.
func TestConfirmNegativeStock(t *testing.T) {
	f := newFixture(t)
	f.addStock(t, f.plain, "10")
	c := f.open(t)
	f.add(t, c.ID, f.plain, "A", "0")
	f.addStock(t, f.plain, "-8") // como una venta después de la foto

	_, err := f.svc.Confirm(f.ctx, c.ID)
	if !errors.Is(err, ErrNegativeStock) || !errors.Is(err, service.ErrConflict) ||
		!strings.Contains(err.Error(), "GASEOSA TEST COUNTS") {
		t.Fatalf("got %v, want %v naming the product", err, ErrNegativeStock)
	}
	assertUntouched(t, f, c.ID, "2")
}

type failingAudit struct{}

func (failingAudit) Create(context.Context, postgres.AuditEntry) (int64, error) {
	return 0, errors.New("audit down")
}

// Si algo falla al final (la auditoría), no queda nada de la confirmación.
func TestConfirmRollsBack(t *testing.T) {
	f := newFixture(t)
	f.addStock(t, f.plain, "10")
	c := f.open(t)
	f.add(t, c.ID, f.plain, "A", "4")
	f.add(t, c.ID, f.batched, "A", "1")
	f.svc.audit = func(postgres.DBTX) auditStore { return failingAudit{} }

	if _, err := f.svc.Confirm(f.ctx, c.ID); err == nil {
		t.Fatal("confirm with failing audit: got no error")
	}
	assertUntouched(t, f, c.ID, "10")
	var batchCount int
	if err := f.tx.QueryRowContext(f.ctx, `SELECT count(*) FROM product_batches WHERE product_id = $1;`,
		f.batched).Scan(&batchCount); err != nil || batchCount != 0 {
		t.Errorf("batches left: %d, %v", batchCount, err)
	}
}

// assertUntouched: el conteo sigue en borrador, sin movimientos, y el saldo
// del producto común es balance.
func assertUntouched(t *testing.T, f *fixture, countID int64, balance string) {
	t.Helper()
	var moves int
	if err := f.tx.QueryRowContext(f.ctx, `SELECT count(*) FROM stock_movements WHERE count_id = $1;`,
		countID).Scan(&moves); err != nil || moves != 0 {
		t.Errorf("movements left: %d, %v", moves, err)
	}
	if got := f.onHand(t, f.plain); !got.Equal(dec(balance)) {
		t.Errorf("balance: got %s, want %s", got, balance)
	}
	if c, err := f.svc.Get(f.ctx, countID); err != nil || c.Status != model.CountDraft {
		t.Errorf("count: got %+v, %v; want still DRAFT", c, err)
	}
}
