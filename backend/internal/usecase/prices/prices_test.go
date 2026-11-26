package prices

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

// fixture: una transacción que se deshace al terminar y un producto activo.
type fixture struct {
	ctx        context.Context
	tx         *sql.Tx
	userID     int64 // quién hace las cosas (F-13)
	svc        *Service
	categoryID int64
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
	if err := tx.QueryRowContext(f.ctx, `INSERT INTO users (username, name, password_hash, role)
		VALUES ('test.usuario', 'Usuario Test', 'x', 'ADMIN') RETURNING id;`).Scan(&f.userID); err != nil {
		t.Fatal(err)
	}
	var taxID int64
	f.scan(t, &taxID, `INSERT INTO taxes (name, rate) VALUES ('IVA test prices', 21) RETURNING id;`)
	f.scan(t, &f.categoryID, `INSERT INTO categories (name, tax_id) VALUES ('Categoria test prices', $1) RETURNING id;`, taxID)
	f.productID = f.newProduct(t, "PRODUCTO TEST PRICES", true)
	return f
}

func (f *fixture) scan(t *testing.T, dest *int64, q string, args ...any) {
	t.Helper()
	if err := f.tx.QueryRowContext(f.ctx, q, args...).Scan(dest); err != nil {
		t.Fatalf("fixture %q: %v", q, err)
	}
}

func (f *fixture) newProduct(t *testing.T, name string, active bool) int64 {
	t.Helper()
	var id int64
	f.scan(t, &id, `INSERT INTO products (name, category_id, is_active) VALUES ($1, $2, $3) RETURNING id;`,
		name, f.categoryID, active)
	return id
}

func (f *fixture) count(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	var n int64
	f.scan(t, &n, q, args...)
	return n
}

func (f *fixture) pricesOf(t *testing.T, productID int64) int64 {
	return f.count(t, `SELECT count(*) FROM prices WHERE product_id = $1;`, productID)
}

func (f *fixture) auditsOf(t *testing.T, priceID int64) int64 {
	return f.count(t, `SELECT count(*) FROM audit_log WHERE table_name = 'prices' AND record_id = $1;`, priceID)
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error: got %v, want %v", got, want)
	}
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestSetSavesPriceAndAudit(t *testing.T) {
	f := newFixture(t)

	got, err := f.svc.Set(f.ctx, f.userID, f.productID, dec("1500.50"))
	if err != nil {
		t.Fatalf("set: %v", err)
	}
	usecasetest.WantAuthor(t, f.ctx, f.tx, "price audit", usecasetest.Author("audit_log", "user_id",
		"table_name = 'prices' AND record_id = $2"), f.userID, got.ID)
	if got.ID == 0 || got.ProductID != f.productID || !got.UnitPrice.Equal(dec("1500.50")) {
		t.Errorf("set: got %+v", got)
	}
	if n := f.auditsOf(t, got.ID); n != 1 {
		t.Errorf("audit rows: got %d, want 1", n)
	}

	var unitPrice string
	if err := f.tx.QueryRowContext(f.ctx,
		`SELECT new_data->>'unit_price' FROM audit_log WHERE table_name = 'prices' AND record_id = $1;`,
		got.ID).Scan(&unitPrice); err != nil {
		t.Fatalf("read audit: %v", err)
	}
	if !dec(unitPrice).Equal(dec("1500.50")) {
		t.Errorf("audit unit_price: got %q", unitPrice)
	}
}

func TestSetNewPriceBecomesCurrent(t *testing.T) {
	f := newFixture(t)

	if _, err := f.svc.Set(f.ctx, f.userID, f.productID, dec("100")); err != nil {
		t.Fatalf("set first: %v", err)
	}
	second, err := f.svc.Set(f.ctx, f.userID, f.productID, dec("120"))
	if err != nil {
		t.Fatalf("set second: %v", err)
	}

	hist, err := f.svc.History(f.ctx, f.productID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if len(hist) != 2 || hist[0].ID != second.ID {
		t.Errorf("history: got %+v, want 2 prices with the newest first", hist)
	}
}

func TestSetInvalidPrice(t *testing.T) {
	f := newFixture(t)

	_, err := f.svc.Set(f.ctx, f.userID, f.productID, dec("0"))
	wantErr(t, err, service.ErrValidation)
	wantErr(t, err, model.ErrInvalidPrice)
	if n := f.pricesOf(t, f.productID); n != 0 {
		t.Errorf("prices saved: %d, want 0", n)
	}
}

func TestSetUnknownProduct(t *testing.T) {
	f := newFixture(t)

	_, err := f.svc.Set(f.ctx, f.userID, 999_999_999, dec("100"))
	wantErr(t, err, service.ErrNotFound)
}

func TestSetInvalidID(t *testing.T) {
	f := newFixture(t)

	_, err := f.svc.Set(f.ctx, f.userID, 0, dec("100"))
	wantErr(t, err, service.ErrInvalidID)
}

func TestSetInactiveProduct(t *testing.T) {
	f := newFixture(t)
	inactive := f.newProduct(t, "INACTIVO TEST PRICES", false)

	_, err := f.svc.Set(f.ctx, f.userID, inactive, dec("100"))
	wantErr(t, err, ErrProductInactive)
	wantErr(t, err, service.ErrConflict)
	if n := f.pricesOf(t, inactive); n != 0 {
		t.Errorf("prices saved: %d, want 0", n)
	}
}

// failingAudit simula que el INSERT en audit_log falla cuando el precio ya se
// guardó en la misma transacción.
type failingAudit struct{}

var errAuditInsert = errors.New("audit insert failed")

func (failingAudit) Create(context.Context, postgres.AuditEntry) (int64, error) {
	return 0, errAuditInsert
}

func TestSetRollsBackPriceWhenAuditFails(t *testing.T) {
	f := newFixture(t)
	f.svc.audit = func(postgres.DBTX) auditStore { return failingAudit{} }

	_, err := f.svc.Set(f.ctx, f.userID, f.productID, dec("100"))
	wantErr(t, err, errAuditInsert)
	if n := f.pricesOf(t, f.productID); n != 0 {
		t.Errorf("prices left after rollback: %d, want 0", n)
	}
}

func TestHistoryEmptyAndUnknown(t *testing.T) {
	f := newFixture(t)

	hist, err := f.svc.History(f.ctx, f.productID)
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if hist == nil || len(hist) != 0 {
		t.Errorf("history: got %#v, want empty non-nil", hist)
	}

	_, err = f.svc.History(f.ctx, 999_999_999)
	wantErr(t, err, service.ErrNotFound)
}

// SetMany: todos o ninguno, cada uno con su auditoría y su autor.
func TestSetMany(t *testing.T) {
	f := newFixture(t)
	a := f.newProduct(t, "PRODUCTO A SETMANY", true)
	b := f.newProduct(t, "PRODUCTO B SETMANY", true)
	off := f.newProduct(t, "PRODUCTO OFF SETMANY", false)

	saved, err := f.svc.SetMany(f.ctx, f.userID, []Change{{a, decimal.RequireFromString("100")}, {b, decimal.RequireFromString("250.5")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 || saved[0].ProductID != a || !saved[1].UnitPrice.Equal(decimal.RequireFromString("250.5")) {
		t.Fatalf("saved: %+v", saved)
	}
	for _, p := range saved {
		if f.auditsOf(t, p.ID) != 1 {
			t.Errorf("price %d: no audit", p.ID)
		}
		usecasetest.WantAuthor(t, f.ctx, f.tx, "set many audit", usecasetest.Author("audit_log", "user_id",
			"table_name = 'prices' AND record_id = $2"), f.userID, p.ID)
	}

	// El segundo falla (inactivo): no se guarda ninguno, y el error dice cuál.
	_, err = f.svc.SetMany(f.ctx, f.userID, []Change{{a, decimal.RequireFromString("110")}, {off, decimal.RequireFromString("5")}})
	var ce *ChangeError
	if !errors.As(err, &ce) || ce.Index != 1 || ce.ProductID != off || !errors.Is(err, ErrProductInactive) {
		t.Errorf("inactive: got %v", err)
	}
	if f.pricesOf(t, a) != 1 || f.pricesOf(t, off) != 0 {
		t.Errorf("rollback: a has %d prices, off %d", f.pricesOf(t, a), f.pricesOf(t, off))
	}
	// Un precio inválido se rechaza antes de empezar.
	_, err = f.svc.SetMany(f.ctx, f.userID, []Change{{b, decimal.RequireFromString("0")}})
	if !errors.As(err, &ce) || ce.Index != 0 || !errors.Is(err, service.ErrValidation) {
		t.Errorf("invalid: got %v", err)
	}
}
