package settings

import (
	"context"
	"database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"

	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

func TestStartupMode(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; run `make test` with the DB up")
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	var admin int64
	if err := tx.QueryRowContext(ctx, `INSERT INTO users (username, name, password_hash, role)
		VALUES ('test.settings', 'Admin Test', 'x', 'ADMIN') RETURNING id;`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	svc := New(tx, postgres.NewSavepointRunner(tx))

	if err := svc.SetStartupMode(ctx, admin, true); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.Get(ctx); err != nil || !got.StartupMode {
		t.Fatalf("after on: %+v, %v", got, err)
	}
	if err := svc.SetStartupMode(ctx, admin, false); err != nil {
		t.Fatal(err)
	}
	if got, err := svc.Get(ctx); err != nil || got.StartupMode {
		t.Fatalf("after off: %+v, %v", got, err)
	}
	// Cada cambio queda auditado con quién.
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM audit_log
		WHERE table_name = 'app_settings' AND user_id = $1 AND new_data->>'key' = 'startup_mode';`, admin).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("audit rows: %d, want 2", n)
	}
}
