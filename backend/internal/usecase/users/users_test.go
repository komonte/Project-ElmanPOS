package users

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/usecasetest"
)

// fixture: una transacción que se deshace, con un admin (quien hace los
// cambios) y un usuario de cada rol.
type fixture struct {
	ctx                    context.Context
	tx                     *sql.Tx
	svc                    *Service
	admin, cashier, second *model.User
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
	f.admin = f.user(t, "test.jefa", model.RoleAdmin)
	f.second = f.user(t, "test.socio", model.RoleAdmin)
	f.cashier = f.user(t, "test.caja", model.RoleCashier)
	return f
}

func (f *fixture) user(t *testing.T, username, role string) *model.User {
	t.Helper()
	u := &model.User{Username: username, Name: "Test " + username, PasswordHash: "x", Role: role, IsActive: true}
	if err := postgres.NewUserStore(f.tx).Create(f.ctx, u); err != nil {
		t.Fatal(err)
	}
	return u
}

func (f *fixture) get(t *testing.T, id int64) *model.User {
	t.Helper()
	u, err := postgres.NewUserStore(f.tx).Get(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func (f *fixture) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.tx.QueryRowContext(f.ctx, q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

// audits cuenta las filas de audit_log de ese usuario y esa acción.
func (f *fixture) audits(t *testing.T, action string, id int64) int {
	t.Helper()
	return f.count(t, `SELECT count(*) FROM audit_log
		WHERE table_name = 'users' AND action = $1 AND record_id = $2;`, action, id)
}

func (f *fixture) wantAuthor(t *testing.T, action string, id int64) {
	t.Helper()
	usecasetest.WantAuthor(t, f.ctx, f.tx, "users "+action, usecasetest.Author("audit_log", "user_id",
		"table_name = 'users' AND action = $2 AND record_id = $3"), f.admin.ID, action, id)
}

func TestCreate(t *testing.T) {
	f := newFixture(t)

	in := &model.User{Username: " Test.Nueva ", Name: "  Ana   Gómez ", Role: model.RoleStockClerk}
	u, err := f.svc.Create(f.ctx, f.admin.ID, in, "clave inicial")
	if err != nil {
		t.Fatal(err)
	}
	got := f.get(t, u.ID)
	if got.Username != "test.nueva" || got.Name != "Ana Gómez" || got.Role != model.RoleStockClerk || !got.IsActive {
		t.Errorf("create: got %+v", got)
	}
	if !model.CheckPassword(got.PasswordHash, "clave inicial") {
		t.Error("create: the initial password does not work")
	}
	f.wantAuthor(t, "INSERT", u.ID)
	if n := f.count(t, `SELECT count(*) FROM audit_log WHERE table_name = 'users' AND action = 'INSERT'
		AND record_id = $1 AND new_data::text LIKE '%$2%';`, u.ID); n != 0 {
		t.Error("create: the password hash is in the audit")
	}

	cases := []struct {
		name     string
		u        *model.User
		password string
		want     error
	}{
		{"bad username", &model.User{Username: "a b", Name: "Ana", Role: model.RoleCashier}, "clave inicial", model.ErrInvalidUsername},
		{"no name", &model.User{Username: "test.x", Name: " ", Role: model.RoleCashier}, "clave inicial", model.ErrEmptyUserName},
		{"bad role", &model.User{Username: "test.x", Name: "Ana", Role: "OWNER"}, "clave inicial", model.ErrInvalidUserRole},
		{"short password", &model.User{Username: "test.x", Name: "Ana", Role: model.RoleCashier}, "corta", model.ErrPasswordTooShort},
	}
	for _, c := range cases {
		if _, err := f.svc.Create(f.ctx, f.admin.ID, c.u, c.password); !errors.Is(err, service.ErrValidation) || !errors.Is(err, c.want) {
			t.Errorf("%s: got %v", c.name, err)
		}
	}

	dup := &model.User{Username: "TEST.NUEVA", Name: "Otra", Role: model.RoleCashier}
	if _, err := f.svc.Create(f.ctx, f.admin.ID, dup, "clave inicial"); !errors.Is(err, service.ErrAlreadyExists) ||
		!errors.Is(err, store.ErrDuplicateUsername) {
		t.Errorf("duplicate: got %v", err)
	}
	if n := f.audits(t, "INSERT", u.ID); n != 1 {
		t.Errorf("only the good one is audited: got %d", n)
	}
}

func TestUpdate(t *testing.T) {
	f := newFixture(t)

	u, err := f.svc.Update(f.ctx, f.admin.ID, f.cashier.ID, "Caja Uno", model.RoleStockClerk)
	if err != nil || u.Name != "Caja Uno" || u.Role != model.RoleStockClerk || u.Username != "test.caja" {
		t.Fatalf("update: got %+v, %v", u, err)
	}
	if got := f.get(t, f.cashier.ID); got.Name != "Caja Uno" || got.Role != model.RoleStockClerk {
		t.Errorf("update saved: got %+v", got)
	}
	f.wantAuthor(t, "UPDATE", f.cashier.ID)
	if n := f.count(t, `SELECT count(*) FROM audit_log WHERE table_name = 'users' AND action = 'UPDATE'
		AND record_id = $1 AND new_data->'before'->>'role' = 'CASHIER' AND new_data->'after'->>'role' = 'STOCK_CLERK';`,
		f.cashier.ID); n != 1 {
		t.Errorf("update audit before/after: got %d", n)
	}

	// Sin cambios: no escribe.
	if _, err := f.svc.Update(f.ctx, f.admin.ID, f.cashier.ID, "Caja  Uno", model.RoleStockClerk); err != nil {
		t.Fatal(err)
	}
	if n := f.audits(t, "UPDATE", f.cashier.ID); n != 1 {
		t.Errorf("no change, no audit: got %d", n)
	}

	// A sí mismo: el nombre sí, el rol no.
	if _, err := f.svc.Update(f.ctx, f.admin.ID, f.admin.ID, "La Jefa", model.RoleAdmin); err != nil {
		t.Errorf("own name: %v", err)
	}
	if _, err := f.svc.Update(f.ctx, f.admin.ID, f.admin.ID, "La Jefa", model.RoleCashier); !errors.Is(err, ErrSelfRole) ||
		!errors.Is(err, service.ErrConflict) {
		t.Errorf("own role: got %v", err)
	}
	if got := f.get(t, f.admin.ID); got.Role != model.RoleAdmin {
		t.Errorf("own role must stay: got %q", got.Role)
	}

	// Otro admin sí puede dejar de serlo (queda la jefa).
	if _, err := f.svc.Update(f.ctx, f.admin.ID, f.second.ID, "Socio", model.RoleCashier); err != nil {
		t.Errorf("demote another admin: %v", err)
	}

	if _, err := f.svc.Update(f.ctx, f.admin.ID, f.cashier.ID, "", model.RoleCashier); !errors.Is(err, service.ErrValidation) {
		t.Errorf("no name: got %v", err)
	}
	if _, err := f.svc.Update(f.ctx, f.admin.ID, f.cashier.ID, "Caja", "OWNER"); !errors.Is(err, model.ErrInvalidUserRole) {
		t.Errorf("bad role: got %v", err)
	}
	if _, err := f.svc.Update(f.ctx, f.admin.ID, -1, "Nadie", model.RoleCashier); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unknown: got %v", err)
	}
}

// El último admin activo no se puede sacar. Con la regla de "a sí mismo"
// solo pasa si quien lo pide ya no es admin: otro admin le sacó el rol
// justo antes (dos admins a la vez). Acá: los demás admins de la base se
// desactivan, la jefa ya es cajera, y su pedido viejo quiere sacar al socio.
func TestLastAdmin(t *testing.T) {
	f := newFixture(t)
	if _, err := f.tx.ExecContext(f.ctx, `UPDATE users SET is_active = FALSE WHERE role = 'ADMIN' AND id <> $1;`, f.second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.tx.ExecContext(f.ctx, `UPDATE users SET role = 'CASHIER', is_active = TRUE WHERE id = $1;`, f.admin.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.Update(f.ctx, f.admin.ID, f.second.ID, "Socio", model.RoleCashier); !errors.Is(err, ErrLastAdmin) ||
		!errors.Is(err, service.ErrConflict) {
		t.Errorf("demote the last admin: got %v", err)
	}
	if err := f.svc.Deactivate(f.ctx, f.admin.ID, f.second.ID); !errors.Is(err, ErrLastAdmin) {
		t.Errorf("deactivate the last admin: got %v", err)
	}
	if got := f.get(t, f.second.ID); got.Role != model.RoleAdmin || !got.IsActive {
		t.Errorf("the last admin must stay: got %+v", got)
	}
	// Cambiarle solo el nombre sí se puede.
	if _, err := f.svc.Update(f.ctx, f.admin.ID, f.second.ID, "Socio", model.RoleAdmin); err != nil {
		t.Errorf("rename the last admin: %v", err)
	}
	// Y a los que no son admin no les afecta.
	if err := f.svc.Deactivate(f.ctx, f.admin.ID, f.cashier.ID); err != nil {
		t.Errorf("deactivate a cashier with one admin left: %v", err)
	}
}

func TestDeactivateReactivate(t *testing.T) {
	f := newFixture(t)
	later := time.Now().Add(time.Hour)
	sessions := postgres.NewUserSessionStore(f.tx)
	for _, token := range []string{"uno", "dos"} {
		if _, err := sessions.Create(f.ctx, f.cashier.ID, hash(token), later); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := sessions.Create(f.ctx, f.admin.ID, hash("jefa"), later); err != nil {
		t.Fatal(err)
	}

	if err := f.svc.Deactivate(f.ctx, f.admin.ID, f.cashier.ID); err != nil {
		t.Fatal(err)
	}
	if f.get(t, f.cashier.ID).IsActive {
		t.Error("deactivate: still active")
	}
	if n := f.count(t, `SELECT count(*) FROM user_sessions WHERE user_id = $1;`, f.cashier.ID); n != 0 {
		t.Errorf("deactivate closes the sessions: %d left", n)
	}
	if n := f.count(t, `SELECT count(*) FROM user_sessions WHERE user_id = $1;`, f.admin.ID); n != 1 {
		t.Errorf("other sessions stay: got %d", n)
	}
	f.wantAuthor(t, "DEACTIVATE", f.cashier.ID)
	if n := f.count(t, `SELECT count(*) FROM audit_log WHERE table_name = 'users' AND action = 'DEACTIVATE'
		AND record_id = $1 AND (new_data->>'sessions_closed')::int = 2;`, f.cashier.ID); n != 1 {
		t.Errorf("deactivate audit: got %d", n)
	}
	// Ya inactivo: no hace nada.
	if err := f.svc.Deactivate(f.ctx, f.admin.ID, f.cashier.ID); err != nil {
		t.Error(err)
	}
	if n := f.audits(t, "DEACTIVATE", f.cashier.ID); n != 1 {
		t.Errorf("deactivate twice: %d audits", n)
	}

	if err := f.svc.Reactivate(f.ctx, f.admin.ID, f.cashier.ID); err != nil {
		t.Fatal(err)
	}
	if !f.get(t, f.cashier.ID).IsActive {
		t.Error("reactivate: still inactive")
	}
	f.wantAuthor(t, "REACTIVATE", f.cashier.ID)
	if err := f.svc.Reactivate(f.ctx, f.admin.ID, f.cashier.ID); err != nil {
		t.Error(err)
	}
	if n := f.audits(t, "REACTIVATE", f.cashier.ID); n != 1 {
		t.Errorf("reactivate twice: %d audits", n)
	}

	if err := f.svc.Deactivate(f.ctx, f.admin.ID, f.admin.ID); !errors.Is(err, ErrSelfDeactivate) {
		t.Errorf("deactivate yourself: got %v", err)
	}
	if err := f.svc.Deactivate(f.ctx, f.admin.ID, -1); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("deactivate unknown: got %v", err)
	}
	if err := f.svc.Reactivate(f.ctx, f.admin.ID, -1); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("reactivate unknown: got %v", err)
	}
	// Otro admin sí (queda la jefa).
	if err := f.svc.Deactivate(f.ctx, f.admin.ID, f.second.ID); err != nil {
		t.Errorf("deactivate another admin: %v", err)
	}
}

func TestListGet(t *testing.T) {
	f := newFixture(t)
	list, err := f.svc.List(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, u := range list {
		if u.ID == f.admin.ID || u.ID == f.cashier.ID || u.ID == f.second.ID {
			found++
		}
	}
	if found != 3 {
		t.Errorf("list: found %d of 3", found)
	}
	if u, err := f.svc.Get(f.ctx, f.cashier.ID); err != nil || u.Username != "test.caja" {
		t.Errorf("get: got %+v, %v", u, err)
	}
	if _, err := f.svc.Get(f.ctx, -1); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("get unknown: got %v", err)
	}
}

func hash(token string) []byte {
	b := make([]byte, 32)
	copy(b, token)
	return b
}
