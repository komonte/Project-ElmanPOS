package productcodes_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	_ "github.com/lib/pq"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/productcodes"
)

// fixture: una transacción que se deshace al terminar (como testTx en el
// store) y dos productos para colgarles códigos.
type fixture struct {
	ctx   context.Context
	tx    *sql.Tx
	svc   *productcodes.Service
	cola  int64
	galle int64
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

	f := &fixture{ctx: context.Background(), tx: tx}
	var taxID, categoryID int64
	f.scan(t, &taxID, `INSERT INTO taxes (name, rate) VALUES ('IVA test codes', 21) RETURNING id;`)
	f.scan(t, &categoryID, `INSERT INTO categories (name, tax_id) VALUES ('Categoria test codes', $1) RETURNING id;`, taxID)
	f.scan(t, &f.cola, `INSERT INTO products (name, category_id) VALUES ('COLA TEST CODES', $1) RETURNING id;`, categoryID)
	f.scan(t, &f.galle, `INSERT INTO products (name, category_id) VALUES ('GALLETITAS TEST CODES', $1) RETURNING id;`, categoryID)

	f.svc = productcodes.New(postgres.NewProductStore(tx), postgres.NewProductCodeStore(tx))
	return f
}

func (f *fixture) scan(t *testing.T, dest *int64, q string, args ...any) {
	t.Helper()
	if err := f.tx.QueryRowContext(f.ctx, q, args...).Scan(dest); err != nil {
		t.Fatalf("fixture %q: %v", q, err)
	}
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error: got %v, want %v", got, want)
	}
}

// Los códigos de prueba empiezan con 0000000977: no chocan con el seed ni
// con el catálogo real.

func TestAddNormalizesAndLists(t *testing.T) {
	f := newFixture(t)

	got, err := f.svc.Add(f.ctx, f.cola, " 9770101\n")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if got.Code != "0000009770101" || got.CodeType != model.CodeTypeInternal || got.ProductID != f.cola {
		t.Errorf("add: got %+v", got)
	}

	list, err := f.svc.List(f.ctx, f.cola)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != got.ID {
		t.Errorf("list: got %+v", list)
	}
}

func TestAddInvalidCode(t *testing.T) {
	f := newFixture(t)

	_, err := f.svc.Add(f.ctx, f.cola, "77-90")
	wantErr(t, err, service.ErrValidation)
	wantErr(t, err, model.ErrBarcodeNotDigits)
}

// El mismo código escrito de otra forma ("9770102" y "0000009770102") es
// el mismo: el error dice de qué producto es.
func TestAddTakenCodeNamesTheOwner(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Add(f.ctx, f.cola, "9770102"); err != nil {
		t.Fatalf("add: %v", err)
	}

	_, err := f.svc.Add(f.ctx, f.galle, "0000009770102")
	wantErr(t, err, service.ErrAlreadyExists)
	wantErr(t, err, store.ErrDuplicateCode)

	var taken *productcodes.CodeTakenError
	if !errors.As(err, &taken) {
		t.Fatalf("got %T %v, want *CodeTakenError", err, err)
	}
	if taken.ProductID != f.cola || taken.ProductName != "COLA TEST CODES" || taken.Code != "0000009770102" {
		t.Errorf("owner: got %+v", taken)
	}

	// El chequeo es antes del INSERT: la transacción no quedó abortada.
	if _, err := f.svc.List(f.ctx, f.galle); err != nil {
		t.Errorf("list after taken code: %v", err)
	}
}

func TestAddToMissingProduct(t *testing.T) {
	f := newFixture(t)

	_, err := f.svc.Add(f.ctx, 999999999, "9770103")
	wantErr(t, err, service.ErrNotFound)
}

func TestListMissingProduct(t *testing.T) {
	f := newFixture(t)

	_, err := f.svc.List(f.ctx, 999999999)
	wantErr(t, err, service.ErrNotFound)
}

func TestRemove(t *testing.T) {
	f := newFixture(t)
	c, err := f.svc.Add(f.ctx, f.cola, "9770104")
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Desde otro producto no se puede quitar.
	wantErr(t, f.svc.Remove(f.ctx, f.galle, c.ID), service.ErrNotFound)

	if err := f.svc.Remove(f.ctx, f.cola, c.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	wantErr(t, f.svc.Remove(f.ctx, f.cola, c.ID), service.ErrNotFound)
}

func TestInvalidIDs(t *testing.T) {
	// No hace falta la DB: se frenan antes.
	svc := productcodes.New(nil, nil)
	ctx := context.Background()

	_, err := svc.List(ctx, 0)
	wantErr(t, err, service.ErrInvalidID)
	_, err = svc.Add(ctx, -1, "9770105")
	wantErr(t, err, service.ErrInvalidID)
	wantErr(t, svc.Remove(ctx, 1, 0), service.ErrInvalidID)
}

// raceStore simula que otro cargó el código entre CheckFree y el INSERT:
// FindByCode no lo encuentra, pero el Add choca con el UNIQUE.
type raceStore struct{}

func (raceStore) FindByCode(context.Context, string) (*model.ProductCode, error) {
	return nil, store.ErrNotFound
}
func (raceStore) ListByProduct(context.Context, int64) ([]*model.ProductCode, error) {
	return nil, nil
}
func (raceStore) Add(context.Context, *model.ProductCode) (*model.ProductCode, error) {
	return nil, store.ErrDuplicateCode
}
func (raceStore) Delete(context.Context, int64, int64) error { return nil }

func TestAddRaceStillReportsDuplicate(t *testing.T) {
	svc := productcodes.New(nil, raceStore{})

	_, err := svc.Add(context.Background(), 1, "9770106")
	wantErr(t, err, service.ErrAlreadyExists)
	var taken *productcodes.CodeTakenError
	if errors.As(err, &taken) {
		t.Errorf("race: got owner %+v, want plain duplicate", taken)
	}
}

func TestCheck(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.Add(f.ctx, f.cola, "9770501"); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Libre: nil.
	if err := f.svc.Check(f.ctx, "9770502"); err != nil {
		t.Errorf("free code: %v", err)
	}

	// Tomado, escrito sin los ceros: dice el dueño.
	err := f.svc.Check(f.ctx, " 9770501 ")
	var taken *productcodes.CodeTakenError
	if !errors.As(err, &taken) || taken.ProductID != f.cola {
		t.Errorf("taken code: got %v, want CodeTakenError for %d", err, f.cola)
	}

	// Mal escrito.
	err = f.svc.Check(f.ctx, "77-90")
	wantErr(t, err, service.ErrValidation)
	wantErr(t, err, model.ErrBarcodeNotDigits)
}
