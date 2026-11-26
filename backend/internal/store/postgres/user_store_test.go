package postgres_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"testing"
	"time"

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
