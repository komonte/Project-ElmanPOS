package combos

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
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

type fixture struct {
	ctx context.Context
	tx  *sql.Tx
	svc *Service

	atado, suelto, queso, otroCombo, varios, inactivo int64
}

// newFixture: una transacción que se deshace al terminar, con un combo
// (atado), productos que pueden ser componentes (suelto por unidad, queso
// por peso) y los que no (otro combo, artículo sin registrar, inactivo).
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

	f := &fixture{ctx: context.Background(), tx: tx, svc: New(tx)}
	var taxID, catID int64
	f.scan(t, &taxID, `INSERT INTO taxes (name, rate) VALUES ('IVA test combos', 21) RETURNING id;`)
	f.scan(t, &catID, `INSERT INTO categories (name, tax_id) VALUES ('Categoria test combos', $1) RETURNING id;`, taxID)
	product := func(name, saleType, unit string, active, open bool) int64 {
		var id int64
		f.scan(t, &id, `INSERT INTO products (name, category_id, sale_type, measure_unit, is_active, open_price)
			VALUES ($1, $2, $3, $4, $5, $6) RETURNING id;`, name, catID, saleType, unit, active, open)
		return id
	}
	f.atado = product("ATADO TEST COMBOS", "COMPOSITE", "UNIT", true, false)
	f.suelto = product("SUELTO TEST COMBOS", "UNIT", "UNIT", true, false)
	f.queso = product("QUESO TEST COMBOS", "WEIGHT", "KILOGRAM", true, false)
	f.otroCombo = product("OTRO COMBO TEST COMBOS", "COMPOSITE", "UNIT", true, false)
	f.varios = product("VARIOS TEST COMBOS", "UNIT", "UNIT", true, true)
	f.inactivo = product("INACTIVO TEST COMBOS", "UNIT", "UNIT", false, false)
	return f
}

func (f *fixture) scan(t *testing.T, dest any, q string, args ...any) {
	t.Helper()
	if err := f.tx.QueryRowContext(f.ctx, q, args...).Scan(dest); err != nil {
		t.Fatalf("fixture %q: %v", q, err)
	}
}

func TestSetListRemove(t *testing.T) {
	f := newFixture(t)
	c, err := f.svc.Set(f.ctx, f.atado, f.suelto, d("20"))
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if c.ComponentName != "SUELTO TEST COMBOS" || !c.Quantity.Equal(d("20")) || !c.ComponentActive {
		t.Errorf("Set returned %+v", c)
	}
	if _, err := f.svc.Set(f.ctx, f.atado, f.queso, d("0.250")); err != nil {
		t.Fatalf("Set weight component: %v", err)
	}
	// Ponerlo de nuevo cambia la cantidad, no agrega otro renglón.
	again, err := f.svc.Set(f.ctx, f.atado, f.suelto, d("10"))
	if err != nil {
		t.Fatalf("Set again: %v", err)
	}
	if again.ID != c.ID || !again.Quantity.Equal(d("10")) {
		t.Errorf("Set again = %+v, want the same row with 10", again)
	}

	list, err := f.svc.List(f.ctx, f.atado)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("List = %d rows, want 2", len(list))
	}

	if err := f.svc.Remove(f.ctx, f.otroCombo, c.ID); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("Remove from another combo: err = %v, want ErrNotFound", err)
	}
	if err := f.svc.Remove(f.ctx, f.atado, c.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if list, _ := f.svc.List(f.ctx, f.atado); len(list) != 1 {
		t.Errorf("after Remove: %d rows, want 1", len(list))
	}
}

func TestSetRejected(t *testing.T) {
	f := newFixture(t)
	tests := []struct {
		name       string
		pack, comp int64
		qty        string
		want       error
	}{
		{"not a combo", f.suelto, f.queso, "1", model.ErrComponentPackNotCombo},
		{"nested combo", f.atado, f.otroCombo, "1", model.ErrComponentIsCombo},
		{"unregistered item", f.atado, f.varios, "1", model.ErrComponentOpenPrice},
		{"inactive", f.atado, f.inactivo, "1", model.ErrComponentInactive},
		{"itself", f.atado, f.atado, "1", model.ErrComponentSelf},
		{"zero", f.atado, f.suelto, "0", model.ErrInvalidComponentQty},
		{"4 decimals", f.atado, f.queso, "0.0001", model.ErrInvalidComponentQty},
		{"fraction of a unit", f.atado, f.suelto, "1.5", model.ErrQuantityNotWhole},
		{"unknown component", f.atado, 999999999, "1", ErrComponentUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.svc.Set(f.ctx, tt.pack, tt.comp, d(tt.qty))
			if !errors.Is(err, tt.want) || !errors.Is(err, service.ErrValidation) {
				t.Errorf("err = %v, want %v (validation)", err, tt.want)
			}
		})
	}
	if _, err := f.svc.Set(f.ctx, 999999999, f.suelto, d("1")); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unknown combo: err = %v, want ErrNotFound", err)
	}
	if _, err := f.svc.List(f.ctx, 999999999); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("List unknown combo: err = %v, want ErrNotFound", err)
	}
}
