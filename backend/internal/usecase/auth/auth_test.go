package auth

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

type fixture struct {
	ctx   context.Context
	tx    *sql.Tx
	svc   *Service
	clock time.Time
}

// newFixture: el caso de uso dentro de una transacción que se deshace, con
// un reloj que el test mueve.
func newFixture(t *testing.T) *fixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; run `make test` with the DB up")
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx (is the DB up?): %v", err)
	}
	t.Cleanup(func() { tx.Rollback() })

	f := &fixture{ctx: context.Background(), tx: tx, clock: time.Now()}
	f.svc = New(tx, postgres.NewSavepointRunner(tx))
	f.svc.now = func() time.Time { return f.clock }
	return f
}

// user guarda un usuario con esa contraseña.
func (f *fixture) user(t *testing.T, username, password string, active bool) *model.User {
	t.Helper()
	hash, err := model.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	u := &model.User{Username: username, Name: "Test", PasswordHash: hash, Role: model.RoleCashier, IsActive: active}
	if err := postgres.NewUserStore(f.tx).Create(f.ctx, u); err != nil {
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

func TestLoginLogout(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "test.cajera", "clave del almacén", true)

	// Un vencido de antes: el login lo limpia.
	if _, err := f.tx.ExecContext(f.ctx, `INSERT INTO user_sessions (user_id, token_hash, created_at, expires_at)
		VALUES ($1, $2, now() - interval '2 days', now() - interval '1 day');`, u.ID, HashToken("viejo")); err != nil {
		t.Fatal(err)
	}

	login, err := f.svc.Login(f.ctx, "  Test.Cajera ", "clave del almacén")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if len(login.Token) != 43 || login.User.ID != u.ID || !login.ExpiresAt.Equal(f.clock.Add(12*time.Hour)) {
		t.Errorf("login: got %d-char token, %+v, %v", len(login.Token), login.User, login.ExpiresAt)
	}
	// En la base está la huella, nunca el token.
	var stored []byte
	if err := f.tx.QueryRowContext(f.ctx, `SELECT token_hash FROM user_sessions WHERE user_id = $1;`, u.ID).Scan(&stored); err != nil {
		t.Fatalf("one session left (the expired one is gone): %v", err)
	}
	if !bytes.Equal(stored, HashToken(login.Token)) || bytes.Contains(stored, []byte(login.Token)) {
		t.Error("the table must keep the SHA-256 of the token")
	}
	if n := f.count(t, `SELECT count(*) FROM audit_log WHERE table_name = 'users' AND action = 'LOGIN'
		AND user_id = $1 AND record_id = $1;`, u.ID); n != 1 {
		t.Errorf("login audit: got %d", n)
	}

	// Cada login da otro token.
	again, err := f.svc.Login(f.ctx, "test.cajera", "clave del almacén")
	if err != nil || again.Token == login.Token {
		t.Errorf("second login: %v", err)
	}

	sess, err := f.svc.Authenticate(f.ctx, login.Token)
	if err != nil || sess.User == nil || sess.User.Username != "test.cajera" {
		t.Fatalf("authenticate: got %+v, %v", sess, err)
	}
	for _, bad := range []string{"", "otro", login.Token + "x"} {
		if _, err := f.svc.Authenticate(f.ctx, bad); !errors.Is(err, ErrNotAuthenticated) {
			t.Errorf("token %q: got %v", bad, err)
		}
	}

	if err := f.svc.Logout(f.ctx, login.Token); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Authenticate(f.ctx, login.Token); !errors.Is(err, ErrNotAuthenticated) {
		t.Errorf("after logout: got %v", err)
	}
	if _, err := f.svc.Authenticate(f.ctx, again.Token); err != nil {
		t.Errorf("the other session stays: %v", err)
	}
	if err := f.svc.Logout(f.ctx, login.Token); err != nil {
		t.Errorf("logout twice: %v", err)
	}
	if err := f.svc.Logout(f.ctx, ""); err != nil {
		t.Errorf("logout without token: %v", err)
	}
}

func TestLoginRejected(t *testing.T) {
	f := newFixture(t)
	f.user(t, "test.activo", "clave correcta", true)
	f.user(t, "test.inactivo", "clave correcta", false)

	for name, c := range map[string][2]string{
		"wrong password":                 {"test.activo", "clave incorrecta"},
		"unknown user":                   {"test.nadie", "clave correcta"},
		"inactive user, right password":  {"test.inactivo", "clave correcta"},
		"empty":                          {"", ""},
		"password with a different case": {"test.activo", "Clave correcta"},
		"password with a trailing space": {"test.activo", "clave correcta "},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := f.svc.Login(f.ctx, c[0], c[1]); !errors.Is(err, ErrInvalidCredentials) {
				t.Errorf("got %v, want %v", err, ErrInvalidCredentials)
			}
		})
	}
	if n := f.count(t, `SELECT count(*) FROM user_sessions us JOIN users u ON u.id = us.user_id
		WHERE u.username LIKE 'test.%';`); n != 0 {
		t.Errorf("no session for a failed login: got %d", n)
	}

	// Un usuario que no existe tarda como uno que existe (compara contra un
	// hash del mismo costo): no se puede saber cuáles hay midiendo.
	dummyHash()
	start := time.Now()
	f.svc.Login(f.ctx, "test.otro.nadie", "clave correcta")
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Errorf("unknown user answered in %v: it must compare a hash too", elapsed)
	}
}

func TestLoginLockout(t *testing.T) {
	f := newFixture(t)
	f.user(t, "test.traba", "clave correcta", true)
	f.user(t, "test.vecino", "clave correcta", true)

	// 4 fallos y un acierto: el acierto borra los fallos.
	for i := 0; i < MaxFailures-1; i++ {
		f.svc.Login(f.ctx, "test.traba", "mal")
	}
	if _, err := f.svc.Login(f.ctx, "test.traba", "clave correcta"); err != nil {
		t.Fatalf("4 failures do not lock: %v", err)
	}

	// 5 seguidos: trabado, aunque después ponga la correcta. Con mayúsculas
	// es el mismo usuario.
	for i := 0; i < MaxFailures; i++ {
		if _, err := f.svc.Login(f.ctx, "TEST.traba", "mal"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("failure %d: got %v", i+1, err)
		}
	}
	if _, err := f.svc.Login(f.ctx, "test.traba", "clave correcta"); !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("locked: got %v, want %v", err, ErrTooManyAttempts)
	}
	// Los demás entran.
	if _, err := f.svc.Login(f.ctx, "test.vecino", "clave correcta"); err != nil {
		t.Errorf("another user is not locked: %v", err)
	}

	f.clock = f.clock.Add(LockoutDuration - time.Second)
	if _, err := f.svc.Login(f.ctx, "test.traba", "clave correcta"); !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("still locked a second before: got %v", err)
	}
	f.clock = f.clock.Add(time.Second)
	if _, err := f.svc.Login(f.ctx, "test.traba", "clave correcta"); err != nil {
		t.Errorf("unlocked after 5 minutes: %v", err)
	}

	// Un usuario que no existe también se traba: si no, se sabría que no existe.
	for i := 0; i < MaxFailures; i++ {
		f.svc.Login(f.ctx, "test.fantasma", "mal")
	}
	if _, err := f.svc.Login(f.ctx, "test.fantasma", "mal"); !errors.Is(err, ErrTooManyAttempts) {
		t.Errorf("unknown user locks too: got %v", err)
	}
}

func TestSetPassword(t *testing.T) {
	f := newFixture(t)
	u := f.user(t, "test.clave", "clave vieja", true)
	login, err := f.svc.Login(f.ctx, "test.clave", "clave vieja")
	if err != nil {
		t.Fatal(err)
	}
	// Trabado por fallos: cambiar la contraseña lo destraba.
	for i := 0; i < MaxFailures; i++ {
		f.svc.Login(f.ctx, "test.clave", "mal")
	}

	if _, err := f.svc.SetPassword(f.ctx, "test.clave", "corta"); !errors.Is(err, service.ErrValidation) ||
		!errors.Is(err, model.ErrPasswordTooShort) {
		t.Errorf("short: got %v", err)
	}
	if _, err := f.svc.SetPassword(f.ctx, "test.nadie", "clave nueva"); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unknown user: got %v", err)
	}

	got, err := f.svc.SetPassword(f.ctx, " Test.Clave", "clave nueva")
	if err != nil || got.ID != u.ID {
		t.Fatalf("set password: got %+v, %v", got, err)
	}
	// Las sesiones abiertas se cierran.
	if _, err := f.svc.Authenticate(f.ctx, login.Token); !errors.Is(err, ErrNotAuthenticated) {
		t.Errorf("old session must be closed: got %v", err)
	}
	if _, err := f.svc.Login(f.ctx, "test.clave", "clave vieja"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("old password: got %v", err)
	}
	if _, err := f.svc.Login(f.ctx, "test.clave", "clave nueva"); err != nil {
		t.Errorf("new password: %v", err)
	}
	if n := f.count(t, `SELECT count(*) FROM audit_log WHERE table_name = 'users' AND action = 'PASSWORD'
		AND record_id = $1 AND user_id IS NULL AND (new_data->>'sessions_closed')::int = 1;`, u.ID); n != 1 {
		t.Errorf("password audit: got %d", n)
	}
}

func TestHashToken(t *testing.T) {
	if len(HashToken("x")) != 32 || bytes.Equal(HashToken("x"), HashToken("y")) {
		t.Error("SHA-256: 32 bytes, different for different tokens")
	}
}
