package postgres_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Usuarios y sesiones del login (F-13).

// testUser guarda un usuario de prueba (el hash no importa acá).
func testUser(t *testing.T, tx *sql.Tx, username, role string) *model.User {
	t.Helper()
	u := &model.User{Username: username, Name: "Test " + username, PasswordHash: "$2a$12$test", Role: role, IsActive: true}
	if err := postgres.NewUserStore(tx).Create(context.Background(), u); err != nil {
		t.Fatalf("create user %q: %v", username, err)
	}
	return u
}

func hashOf(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func TestUserStore(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	users := postgres.NewUserStore(tx)

	u := testUser(t, tx, "test.cajera", model.RoleCashier)
	if u.ID == 0 || u.CreatedAt == "" {
		t.Fatalf("create: got %+v", u)
	}

	for _, name := range []string{"test.cajera", "TEST.Cajera"} {
		got, err := users.ByUsername(ctx, name)
		if err != nil || got.ID != u.ID || got.Role != model.RoleCashier || got.PasswordHash != "$2a$12$test" ||
			!got.IsActive || got.Name != "Test test.cajera" {
			t.Errorf("by username %q: got %+v, %v", name, got, err)
		}
	}
	if _, err := users.ByUsername(ctx, "test.nadie"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown username: got %v", err)
	}

	got, err := users.Get(ctx, u.ID)
	if err != nil || got.Username != "test.cajera" {
		t.Errorf("get: got %+v, %v", got, err)
	}
	if _, err := users.Get(ctx, -1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get unknown: got %v", err)
	}

	if err := users.SetPassword(ctx, u.ID, "$2a$12$nuevo"); err != nil {
		t.Fatal(err)
	}
	if got, _ := users.Get(ctx, u.ID); got.PasswordHash != "$2a$12$nuevo" {
		t.Errorf("set password: got %q", got.PasswordHash)
	}
	if err := users.SetPassword(ctx, -1, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("set password unknown: got %v", err)
	}

	// Último: el error aborta la transacción.
	dup := &model.User{Username: "Test.Cajera", Name: "Otra", PasswordHash: "x", Role: model.RoleAdmin}
	if err := users.Create(ctx, dup); !errors.Is(err, store.ErrDuplicateUsername) || !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("duplicate ignoring case: got %v", err)
	}
}

// La página de usuarios: listar, editar y desactivar.
func TestUserStoreAdmin(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	users := postgres.NewUserStore(tx)

	zoe := testUser(t, tx, "test.zoe", model.RoleCashier)
	ana := testUser(t, tx, "test.ana", model.RoleStockClerk)
	exec(t, tx, `UPDATE users SET name = 'Ana' WHERE id = $1;`, ana.ID)
	exec(t, tx, `UPDATE users SET name = 'zoe' WHERE id = $1;`, zoe.ID)

	if err := users.Update(ctx, zoe.ID, "Zoe Pérez", model.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	got, _ := users.Get(ctx, zoe.ID)
	if got.Name != "Zoe Pérez" || got.Role != model.RoleAdmin || got.Username != "test.zoe" {
		t.Errorf("update: got %+v", got)
	}
	if err := users.Update(ctx, -1, "Nadie", model.RoleAdmin); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("update unknown: got %v", err)
	}

	if err := users.SetActive(ctx, ana.ID, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := users.Get(ctx, ana.ID); got.IsActive {
		t.Error("set active false: still active")
	}
	if err := users.SetActive(ctx, -1, false); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("set active unknown: got %v", err)
	}

	// Activos primero; cada grupo por nombre sin importar mayúsculas. Ana,
	// inactiva, va después de Zoe aunque su nombre va antes.
	list, err := users.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[int64]int{}
	for i, u := range list {
		pos[u.ID] = i
		if i > 0 && !list[i-1].IsActive && u.IsActive {
			t.Errorf("list: inactive %q before active %q", list[i-1].Username, u.Username)
		}
	}
	zi, okZ := pos[zoe.ID]
	ai, okA := pos[ana.ID]
	if !okZ || !okA || zi > ai || list[ai].IsActive {
		t.Errorf("list: zoe at %d (%v), ana at %d (%v)", zi, okZ, ai, okA)
	}

	// Solo los admins activos: Zoe sí; Ana (inactiva) y los cajeros no.
	exec(t, tx, `UPDATE users SET role = 'ADMIN' WHERE id = $1;`, ana.ID)
	ids, err := users.LockActiveAdmins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var want []int64
	for _, u := range list {
		if u.ID != ana.ID && u.IsActive && u.Role == model.RoleAdmin {
			want = append(want, u.ID)
		}
	}
	slices.Sort(want)
	if !slices.Equal(ids, want) || !slices.Contains(ids, zoe.ID) {
		t.Errorf("lock active admins: got %v, want %v", ids, want)
	}
}

// LockActiveAdmins bloquea de verdad: otra conexión no puede tomar la fila
// de un admin hasta que la transacción termina. Usa los admins que ya están
// en la base (los de la transacción no se ven desde otra conexión).
func TestUserStoreLockActiveAdmins(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	ids, err := postgres.NewUserStore(tx).LockActiveAdmins(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) == 0 {
		t.Skip("no active admin in the DB")
	}

	other := testDB(t)
	_, err = other.ExecContext(ctx, `SELECT id FROM users WHERE id = $1 FOR UPDATE NOWAIT;`, ids[0])
	var pqErr *pq.Error
	if !errors.As(err, &pqErr) || pqErr.Code != "55P03" { // lock_not_available
		t.Errorf("another connection took the admin row: got %v", err)
	}
}

func TestUserSessionStore(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	sessions := postgres.NewUserSessionStore(tx)
	u := testUser(t, tx, "test.sesion", model.RoleAdmin)
	other := testUser(t, tx, "test.otro", model.RoleCashier)
	later := time.Now().Add(12 * time.Hour)

	sess, err := sessions.Create(ctx, u.ID, hashOf("token-a"), later)
	if err != nil || sess.ID == 0 || sess.UserID != u.ID || sess.ExpiresAt == "" {
		t.Fatalf("create: got %+v, %v", sess, err)
	}
	if _, err := sessions.Create(ctx, u.ID, hashOf("token-b"), later); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Create(ctx, other.ID, hashOf("token-c"), later); err != nil {
		t.Fatal(err)
	}

	got, err := sessions.Valid(ctx, hashOf("token-a"))
	if err != nil || got.ID != sess.ID || got.User == nil || got.User.ID != u.ID || got.User.Role != model.RoleAdmin {
		t.Fatalf("valid: got %+v, %v", got, err)
	}
	if _, err := sessions.Valid(ctx, hashOf("token-x")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown token: got %v", err)
	}

	// Vencida: no vale, y DeleteExpired la borra (solo a ella).
	exec(t, tx, `UPDATE user_sessions SET created_at = now() - interval '13 hours', expires_at = now() - interval '1 hour'
		WHERE token_hash = $1;`, hashOf("token-b"))
	if _, err := sessions.Valid(ctx, hashOf("token-b")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expired: got %v", err)
	}
	if n, err := sessions.DeleteExpired(ctx); err != nil || n != 1 {
		t.Errorf("delete expired: got %d, %v", n, err)
	}
	if _, err := sessions.Valid(ctx, hashOf("token-a")); err != nil {
		t.Errorf("a live session survives delete expired: %v", err)
	}

	// Usuario desactivado: su sesión deja de valer en el momento.
	exec(t, tx, `UPDATE users SET is_active = FALSE WHERE id = $1;`, other.ID)
	if _, err := sessions.Valid(ctx, hashOf("token-c")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("inactive user: got %v", err)
	}
	if n, err := sessions.DeleteForUser(ctx, other.ID); err != nil || n != 1 {
		t.Errorf("delete for user: got %d, %v", n, err)
	}

	// Salir: borra esa sesión; salir de nuevo no falla.
	if err := sessions.Delete(ctx, hashOf("token-a")); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.Valid(ctx, hashOf("token-a")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("after logout: got %v", err)
	}
	if err := sessions.Delete(ctx, hashOf("token-a")); err != nil {
		t.Errorf("logout twice: %v", err)
	}

	// Último: el error aborta la transacción.
	if _, err := sessions.Create(ctx, -1, hashOf("token-d"), later); !errors.Is(err, store.ErrInvalidReference) {
		t.Errorf("unknown user: got %v", err)
	}
}

func exec(t *testing.T, tx *sql.Tx, q string, args ...any) {
	t.Helper()
	if _, err := tx.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}
