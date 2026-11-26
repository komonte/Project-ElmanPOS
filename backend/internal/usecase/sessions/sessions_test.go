package sessions

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
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/usecasetest"
)

// fixture: una transacción que se deshace al terminar, con un solo local
// activo y sin cajas.
type fixture struct {
	ctx     context.Context
	tx      *sql.Tx
	userID  int64 // quién hace las cosas (F-13)
	svc     *Service
	storeID int64
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
	if err := tx.QueryRowContext(f.ctx,
		`INSERT INTO stores (name) VALUES ('Local test sessions') RETURNING id;`).Scan(&f.storeID); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.tx.ExecContext(f.ctx, q, args...); err != nil {
		t.Fatalf("fixture %q: %v", q, err)
	}
}

func TestOpenFlow(t *testing.T) {
	f := newFixture(t)

	st, err := f.svc.Current(f.ctx)
	if err != nil || st.Register != nil || st.Session != nil {
		t.Fatalf("no register: got %+v, %v", st, err)
	}
	if _, err := f.svc.Open(f.ctx, f.userID, decimal.Zero); !errors.Is(err, ErrNoRegister) || !errors.Is(err, service.ErrConflict) {
		t.Errorf("open without register: got %v, want %v", err, ErrNoRegister)
	}

	reg, err := f.svc.CreateRegister(f.ctx)
	if err != nil || reg.Name != DefaultRegisterName || reg.StoreID != f.storeID {
		t.Fatalf("create register: got %+v, %v", reg, err)
	}
	if again, err := f.svc.CreateRegister(f.ctx); err != nil || again.ID != reg.ID {
		t.Errorf("second create must return the same register: got %+v, %v", again, err)
	}

	st, err = f.svc.Current(f.ctx)
	if err != nil || st.Register == nil || st.Register.ID != reg.ID || st.Session != nil || st.LastClosed != nil {
		t.Fatalf("closed register: got %+v, %v", st, err)
	}

	if _, err := f.svc.Open(f.ctx, f.userID, decimal.RequireFromString("-1")); !errors.Is(err, service.ErrValidation) ||
		!errors.Is(err, model.ErrInvalidOpeningAmount) {
		t.Errorf("negative float: got %v", err)
	}

	sess, err := f.svc.Open(f.ctx, f.userID, decimal.RequireFromString("15000"))
	if err != nil || sess.CashRegisterID != reg.ID || sess.Status != model.SessionOpen ||
		!sess.OpeningAmount.Equal(decimal.RequireFromString("15000")) || sess.RegisterName != DefaultRegisterName {
		t.Fatalf("open: got %+v, %v", sess, err)
	}
	usecasetest.WantAuthor(t, f.ctx, f.tx, "opened by", usecasetest.Author("cashier_sessions", "opened_by", "id = $2"), f.userID, sess.ID)
	st, err = f.svc.Current(f.ctx)
	if err != nil || st.Session == nil || st.Session.ID != sess.ID {
		t.Errorf("open register: got %+v, %v", st, err)
	}

	// La segunda apertura falla en la DB: va al final (la transacción del
	// test queda abortada).
	_, err = f.svc.Open(f.ctx, f.userID, decimal.Zero)
	if !errors.Is(err, service.ErrConflict) || !errors.Is(err, store.ErrSessionAlreadyOpen) {
		t.Errorf("second open: got %v, want %v", err, store.ErrSessionAlreadyOpen)
	}
}

func TestSeveralRegisters(t *testing.T) {
	f := newFixture(t)
	f.exec(t, `INSERT INTO cash_registers (store_id, name) VALUES ($1, 'A'), ($1, 'B');`, f.storeID)
	if _, err := f.svc.Current(f.ctx); !errors.Is(err, store.ErrSeveralActiveRegisters) ||
		!errors.Is(err, service.ErrConflict) {
		t.Errorf("current: got %v", err)
	}
	if _, err := f.svc.CreateRegister(f.ctx); !errors.Is(err, store.ErrSeveralActiveRegisters) {
		t.Errorf("create register: got %v", err)
	}
	if _, err := f.svc.Open(f.ctx, f.userID, decimal.Zero); !errors.Is(err, store.ErrSeveralActiveRegisters) {
		t.Errorf("open: got %v", err)
	}
}

func TestNoActiveStore(t *testing.T) {
	f := newFixture(t)
	f.exec(t, `UPDATE stores SET is_active = FALSE;`)
	if _, err := f.svc.Current(f.ctx); !errors.Is(err, store.ErrNoActiveStore) || !errors.Is(err, service.ErrConflict) {
		t.Errorf("current: got %v", err)
	}
	if _, err := f.svc.CreateRegister(f.ctx); !errors.Is(err, store.ErrNoActiveStore) {
		t.Errorf("create register: got %v", err)
	}
}
