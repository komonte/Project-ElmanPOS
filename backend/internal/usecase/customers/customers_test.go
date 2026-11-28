package customers

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	_ "github.com/lib/pq"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

func newSvc(t *testing.T) (*Service, *sql.Tx, int64) {
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
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	var admin int64
	if err := tx.QueryRow(`INSERT INTO users (username, name, password_hash, role)
		VALUES ('test.customers', 'Admin Test', 'x', 'ADMIN') RETURNING id;`).Scan(&admin); err != nil {
		t.Fatal(err)
	}
	return New(tx, postgres.NewSavepointRunner(tx)), tx, admin
}

func TestCustomers(t *testing.T) {
	svc, tx, admin := newSvc(t)
	ctx := context.Background()

	c, err := svc.Create(ctx, admin, &model.Customer{Name: " Zulma  Test Clientes ", Phone: "11 5555"})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == 0 || c.Name != "Zulma Test Clientes" || !c.IsActive || c.CreatedBy == nil || *c.CreatedBy != admin {
		t.Errorf("created: %+v", c)
	}
	// El mismo nombre, con otras mayúsculas y espacios, no entra.
	if _, err := svc.Create(ctx, admin, &model.Customer{Name: "zulma test clientes"}); !errors.Is(err, ErrNameTaken) ||
		!errors.Is(err, service.ErrConflict) {
		t.Errorf("duplicate: err %v", err)
	}
	if _, err := svc.Create(ctx, admin, &model.Customer{Name: "Z"}); !errors.Is(err, service.ErrValidation) {
		t.Errorf("short: err %v", err)
	}

	up, err := svc.Update(ctx, admin, c.ID, &model.Customer{Name: "Zulma Test Clientes (kiosco)", Notes: "paga los viernes"})
	if err != nil {
		t.Fatal(err)
	}
	if up.Name != "Zulma Test Clientes (kiosco)" || up.Phone != "" || up.Notes != "paga los viernes" {
		t.Errorf("updated: %+v", up)
	}
	if _, err := svc.Update(ctx, admin, 999999999, &model.Customer{Name: "Nadie"}); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("update missing: err %v", err)
	}

	found, err := svc.Search(ctx, "test clientes")
	if err != nil || len(found) != 1 {
		t.Fatalf("search: %v, %d", err, len(found))
	}
	if err := svc.SetActive(ctx, admin, c.ID, false); err != nil {
		t.Fatal(err)
	}
	if found, _ := svc.Search(ctx, "test clientes"); len(found) != 0 {
		t.Error("an inactive customer is offered at the register")
	}
	if list, _ := svc.List(ctx, "test clientes"); len(list) != 1 || list[0].IsActive {
		t.Errorf("list keeps the inactive one: %+v", list)
	}
	if err := svc.SetActive(ctx, admin, 999999999, true); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("set active missing: err %v", err)
	}

	var n int
	if err := tx.QueryRow(`SELECT count(*) FROM audit_log WHERE table_name = 'customers' AND user_id = $1;`, admin).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Errorf("audit rows %d, want 3 (create, update, deactivate)", n)
	}
}
