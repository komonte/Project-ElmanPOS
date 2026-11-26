package stockcounts

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
// activo (los demás se apagan dentro de la transacción), una categoría y
// tres productos: uno común, uno que controla lotes (por peso) y un pack.
type fixture struct {
	ctx        context.Context
	tx         *sql.Tx
	userID     int64 // quién hace las cosas (F-13)
	svc        *Service
	categoryID int64
	storeID    int64
	plain      int64 // sin lotes
	batched    int64 // con lotes, por kg
	composite  int64 // pack: sin stock propio
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
	f.scan(t, &f.storeID, `INSERT INTO stores (name) VALUES ('Local test counts') RETURNING id;`)
	var taxID int64
	f.scan(t, &taxID, `INSERT INTO taxes (name, rate) VALUES ('IVA test counts', 21) RETURNING id;`)
	f.scan(t, &f.categoryID, `INSERT INTO categories (name, tax_id) VALUES ('Categoria test counts', $1) RETURNING id;`, taxID)
	f.plain = f.newProduct(t, "GASEOSA TEST COUNTS", `'UNIT', 'UNIT', FALSE, 30, TRUE`)
	f.batched = f.newProduct(t, "QUESO TEST COUNTS", `'WEIGHT', 'KILOGRAM', TRUE, 30, TRUE`)
	f.composite = f.newProduct(t, "PACK TEST COUNTS", `'COMPOSITE', 'UNIT', FALSE, 30, TRUE`)
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

// addStock suma saldo al producto en el local del fixture (como haría una
// compra, sin el resto).
func (f *fixture) addStock(t *testing.T, productID int64, qty string) {
	t.Helper()
	if _, err := postgres.NewStockBalanceStore(f.tx).Add(f.ctx, productID, f.storeID, dec(qty)); err != nil {
		t.Fatalf("add stock: %v", err)
	}
}

func (f *fixture) open(t *testing.T) *model.StockCount {
	t.Helper()
	c, err := f.svc.Open(f.ctx, f.userID)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	usecasetest.WantAuthor(t, f.ctx, f.tx, "count opened by", usecasetest.Author("stock_counts", "created_by", "id = $2"), f.userID, c.ID)
	return c
}

func (f *fixture) add(t *testing.T, countID, productID int64, sector, qty string) *model.StockCountItem {
	t.Helper()
	it, err := f.svc.AddEntry(f.ctx, countID, &model.StockCountEntry{ProductID: productID, Sector: sector, Quantity: dec(qty)})
	if err != nil {
		t.Fatalf("add entry: %v", err)
	}
	return it
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestOpen(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Current(f.ctx); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("current before open: got %v, want %v", err, service.ErrNotFound)
	}

	c := f.open(t)
	if c.ID == 0 || c.StoreID != f.storeID || c.Status != model.CountDraft || len(c.Items) != 0 {
		t.Errorf("open: got %+v", c)
	}
	// Abrir de nuevo sigue el mismo borrador.
	if again := f.open(t); again.ID != c.ID {
		t.Errorf("open twice: got count %d, want %d", again.ID, c.ID)
	}
	if cur, err := f.svc.Current(f.ctx); err != nil || cur.ID != c.ID {
		t.Errorf("current: got %+v, %v", cur, err)
	}
}

func TestOpenWithoutActiveStore(t *testing.T) {
	f := newFixture(t)
	f.exec(t, `UPDATE stores SET is_active = FALSE;`)
	if _, err := f.svc.Open(f.ctx, f.userID); !errors.Is(err, service.ErrConflict) {
		t.Errorf("got %v, want %v", err, service.ErrConflict)
	}
}

// La foto del saldo se saca en la primera carga y no cambia aunque después
// entre stock; las cargas de distintos sectores se suman.
func TestAddEntry(t *testing.T) {
	f := newFixture(t)
	f.addStock(t, f.plain, "10")
	c := f.open(t)

	entry := &model.StockCountEntry{ProductID: f.plain, Sector: "  Góndola   3 ", Quantity: dec("3")}
	it, err := f.svc.AddEntry(f.ctx, c.ID, entry)
	if err != nil {
		t.Fatalf("first entry: %v", err)
	}
	if !it.SystemQuantity.Equal(dec("10")) || !it.CountedQuantity.Equal(dec("3")) {
		t.Errorf("first entry: got system %s counted %s, want 10 and 3", it.SystemQuantity, it.CountedQuantity)
	}
	if entry.Sector != "  Góndola   3 " {
		t.Errorf("caller's entry changed: sector %q", entry.Sector)
	}

	f.addStock(t, f.plain, "5") // entra una compra en medio del conteo
	it = f.add(t, c.ID, f.plain, "Depósito", "4")
	if !it.SystemQuantity.Equal(dec("10")) || !it.CountedQuantity.Equal(dec("7")) {
		t.Errorf("second entry: got system %s counted %s, want 10 and 7", it.SystemQuantity, it.CountedQuantity)
	}

	// Inventario inicial: sin saldo, la foto es 0.
	if it := f.add(t, c.ID, f.batched, "Heladera", "2.345"); !it.SystemQuantity.IsZero() {
		t.Errorf("no stock: got system %s, want 0", it.SystemQuantity)
	}

	got, err := f.svc.Get(f.ctx, c.ID)
	if err != nil || len(got.Items) != 2 {
		t.Fatalf("get: got %+v, %v", got, err)
	}
	g := got.Items[0] // GASEOSA antes que QUESO
	if g.ProductID != f.plain || len(g.Entries) != 2 || g.Entries[0].Sector != "Góndola 3" {
		t.Errorf("get: got %+v", g)
	}
	if sectors, _ := f.svc.Sectors(f.ctx); len(sectors) < 3 {
		t.Errorf("sectors: got %v", sectors)
	}
}

func TestAddEntryErrors(t *testing.T) {
	f := newFixture(t)
	c := f.open(t)
	inactive := f.newProduct(t, "INACTIVO TEST COUNTS", `'UNIT', 'UNIT', FALSE, 30, FALSE`)

	tests := []struct {
		name    string
		countID int64
		entry   *model.StockCountEntry
		wantErr error
	}{
		{"nil entry", c.ID, nil, service.ErrValidation},
		{"empty sector", c.ID, &model.StockCountEntry{ProductID: f.plain, Sector: " ", Quantity: dec("1")}, model.ErrStockCountSectorEmpty},
		{"negative", c.ID, &model.StockCountEntry{ProductID: f.plain, Sector: "A", Quantity: dec("-1")}, service.ErrValidation},
		{"unknown product", c.ID, &model.StockCountEntry{ProductID: 999999999, Sector: "A", Quantity: dec("1")}, ErrUnknownProduct},
		{"inactive product", c.ID, &model.StockCountEntry{ProductID: inactive, Sector: "A", Quantity: dec("1")}, ErrProductInactive},
		{"composite", c.ID, &model.StockCountEntry{ProductID: f.composite, Sector: "A", Quantity: dec("1")}, ErrCompositeProduct},
		{"missing count", 999999999, &model.StockCountEntry{ProductID: f.plain, Sector: "A", Quantity: dec("1")}, service.ErrNotFound},
		{"bad count id", 0, &model.StockCountEntry{ProductID: f.plain, Sector: "A", Quantity: dec("1")}, service.ErrInvalidID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := f.svc.AddEntry(f.ctx, tt.countID, tt.entry); !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
	if got, _ := f.svc.Get(f.ctx, c.ID); len(got.Items) != 0 {
		t.Errorf("failed entries left items: %+v", got.Items)
	}
}

// Un conteo confirmado no acepta cargas, ni quitar, ni detalles, ni
// descartarse.
func TestConfirmedCountIsClosed(t *testing.T) {
	f := newFixture(t)
	c := f.open(t)
	it := f.add(t, c.ID, f.plain, "A", "1")
	got, _ := f.svc.Get(f.ctx, c.ID)
	entryID := got.Items[0].Entries[0].ID
	if _, err := postgres.NewStockCountStore(f.tx).Confirm(f.ctx, c.ID); err != nil {
		t.Fatal(err)
	}

	checks := map[string]error{
		"add": func() error {
			_, err := f.svc.AddEntry(f.ctx, c.ID, &model.StockCountEntry{ProductID: f.plain, Sector: "A", Quantity: dec("1")})
			return err
		}(),
		"remove":  f.svc.RemoveEntry(f.ctx, c.ID, entryID),
		"details": f.svc.SetDetails(f.ctx, c.ID, it.ProductID, nil, ""),
		"discard": f.svc.Discard(f.ctx, c.ID),
	}
	for name, err := range checks {
		if !errors.Is(err, service.ErrConflict) {
			t.Errorf("%s: got %v, want %v", name, err, service.ErrConflict)
		}
	}
	// Con el confirmado cerrado, Open abre uno nuevo.
	if next := f.open(t); next.ID == c.ID {
		t.Error("open after confirm returned the confirmed count")
	}
}

func TestRemoveEntry(t *testing.T) {
	f := newFixture(t)
	c := f.open(t)
	f.add(t, c.ID, f.plain, "A", "2")
	f.add(t, c.ID, f.plain, "B", "5")
	got, _ := f.svc.Get(f.ctx, c.ID)
	first, second := got.Items[0].Entries[0].ID, got.Items[0].Entries[1].ID

	if err := f.svc.RemoveEntry(f.ctx, c.ID, first); err != nil {
		t.Fatalf("remove first: %v", err)
	}
	got, _ = f.svc.Get(f.ctx, c.ID)
	if len(got.Items) != 1 || !got.Items[0].CountedQuantity.Equal(dec("5")) {
		t.Errorf("after first: got %+v", got.Items)
	}
	if err := f.svc.RemoveEntry(f.ctx, c.ID, first); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("remove twice: got %v, want %v", err, service.ErrNotFound)
	}
	if err := f.svc.RemoveEntry(f.ctx, c.ID, second); err != nil {
		t.Fatalf("remove last: %v", err)
	}
	if got, _ = f.svc.Get(f.ctx, c.ID); len(got.Items) != 0 {
		t.Errorf("after last: product still counted: %+v", got.Items)
	}
}

func TestSetDetails(t *testing.T) {
	f := newFixture(t)
	c := f.open(t)
	f.add(t, c.ID, f.plain, "A", "2")
	f.add(t, c.ID, f.batched, "Heladera", "1.5")
	cost := dec("850.50")

	if err := f.svc.SetDetails(f.ctx, c.ID, f.plain, &cost, ""); err != nil {
		t.Fatalf("cost: %v", err)
	}
	if err := f.svc.SetDetails(f.ctx, c.ID, f.batched, nil, " 2027-03-31 "); err != nil {
		t.Fatalf("expiry: %v", err)
	}
	got, _ := f.svc.Get(f.ctx, c.ID)
	g, q := got.Items[0], got.Items[1] // GASEOSA, QUESO
	if g.UnitCost == nil || !g.UnitCost.Equal(cost) || q.ExpiryDate != "2027-03-31" || q.UnitCost != nil {
		t.Errorf("details: got %+v / %+v", g, q)
	}

	zero := dec("0")
	tests := []struct {
		name      string
		productID int64
		cost      *decimal.Decimal
		expiry    string
		wantErr   error
	}{
		{"expiry without batches", f.plain, nil, "2027-03-31", ErrExpiryNotAllowed},
		{"zero cost", f.plain, &zero, "", model.ErrInvalidCost},
		{"bad expiry", f.batched, nil, "31/03/2027", service.ErrValidation},
		{"product not counted", f.composite, nil, "", service.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := f.svc.SetDetails(f.ctx, c.ID, tt.productID, tt.cost, tt.expiry); !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestDiscard(t *testing.T) {
	f := newFixture(t)
	f.addStock(t, f.plain, "10")
	c := f.open(t)
	f.add(t, c.ID, f.plain, "A", "3")

	if err := f.svc.Discard(f.ctx, c.ID); err != nil {
		t.Fatalf("discard: %v", err)
	}
	if _, err := f.svc.Current(f.ctx); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("current after discard: got %v, want %v", err, service.ErrNotFound)
	}
	if err := f.svc.Discard(f.ctx, c.ID); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("discard twice: got %v, want %v", err, service.ErrNotFound)
	}
	// El stock no se tocó.
	if onHand, _ := postgres.NewStockBalanceStore(f.tx).OnHandAt(f.ctx, f.plain, f.storeID); !onHand.Equal(dec("10")) {
		t.Errorf("stock after discard: got %s, want 10", onHand)
	}
}
