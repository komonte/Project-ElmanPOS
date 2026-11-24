package postgres_test

import (
	"context"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Update no cambia is_active: un PUT de la API sin "is_active" llega con
// false (el valor cero) y desactivaba la marca. Activar y desactivar pasan
// solo por SetActive.
func TestBrandStore_UpdateKeepsIsActive(t *testing.T) {
	ctx := context.Background()
	s := postgres.NewBrandStore(testTx(t))

	b, err := s.Create(ctx, &model.Brand{Name: "MARCA TEST STORE"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Activa + IsActive false en el pedido: sigue activa.
	updated, err := s.Update(ctx, b.ID, &model.Brand{Name: "MARCA TEST STORE 2", IsActive: false})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !updated.IsActive {
		t.Fatal("update deactivated the brand")
	}

	// Inactiva + IsActive true en el pedido: sigue inactiva.
	if err := s.SetActive(ctx, b.ID, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	updated, err = s.Update(ctx, b.ID, &model.Brand{Name: "MARCA TEST STORE 3", IsActive: true})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.IsActive {
		t.Fatal("update reactivated the brand")
	}
}

// ListAll de las tablas maestras simples: misma forma en marcas, impuestos,
// proveedores y categorías. Se prueba con un nombre que no existe en el seed.
func TestMasterStores_ListAll(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)

	var taxID int64
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO taxes (name, rate) VALUES ('ZZLIST IVA', 21) RETURNING id;`).Scan(&taxID); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO brands (name) VALUES ('ZZLIST A'), ('ZZLIST B');`,
		`INSERT INTO taxes (name, rate) VALUES ('ZZLIST IVA 2', 10.5);`,
		`INSERT INTO suppliers (name) VALUES ('ZZLIST Uno'), ('ZZLIST Dos');`,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO categories (name, tax_id) VALUES ('ZZLIST Una', $1), ('ZZLIST Dos', $1);`, taxID); err != nil {
		t.Fatal(err)
	}

	check := func(name string, rows, total int, err error) {
		t.Helper()
		if err != nil || total != 2 || rows != 1 {
			t.Errorf("%s: got %d rows, total %d, err %v; want 1 row, total 2", name, rows, total, err)
		}
	}
	b, n, err := postgres.NewBrandStore(tx).ListAll(ctx, "zzlist", 1)
	check("brands", len(b), n, err)
	x, n, err := postgres.NewTaxStore(tx).ListAll(ctx, "zzlist", 1)
	check("taxes", len(x), n, err)
	s, n, err := postgres.NewSupplierStore(tx).ListAll(ctx, "zzlist", 1)
	check("suppliers", len(s), n, err)
	c, n, err := postgres.NewCategoryStore(tx).ListAll(ctx, "zzlist", 1)
	check("categories", len(c), n, err)
}

func TestBrandStore_GetByName(t *testing.T) {
	ctx := context.Background()
	s := postgres.NewBrandStore(testTx(t))
	created, err := s.Create(ctx, &model.Brand{Name: "MARCA TEST GETBYNAME"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.GetByName(ctx, "MARCA TEST GETBYNAME")
	if err != nil || got.ID != created.ID {
		t.Fatalf("exact name: got %+v, %v", got, err)
	}

	// Exacto, no "contiene".
	_, err = s.GetByName(ctx, "MARCA TEST")
	wantErr(t, err, store.ErrNotFound)
}
