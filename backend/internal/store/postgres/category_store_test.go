package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// categoryFixture arma un store dentro de una transacción y un impuesto
// propio para no depender de los datos del seed.
type categoryFixture struct {
	ctx   context.Context
	tx    *sql.Tx
	store *postgres.CategoryStore
	taxID int64
}

func newCategoryFixture(t *testing.T) *categoryFixture {
	t.Helper()
	tx := testTx(t)
	ctx := context.Background()

	var taxID int64
	err := tx.QueryRowContext(ctx,
		`INSERT INTO taxes (name, rate) VALUES ('IVA test store', 21) RETURNING id;`).Scan(&taxID)
	if err != nil {
		t.Fatalf("insert tax: %v", err)
	}

	return &categoryFixture{ctx: ctx, tx: tx, store: postgres.NewCategoryStore(tx), taxID: taxID}
}

// create guarda una categoría que el test necesita como dato previo; si
// falla, el test no tiene sentido y se corta.
func (f *categoryFixture) create(t *testing.T, name string, parentID *int64) *model.Category {
	t.Helper()
	c, err := f.store.Create(f.ctx, &model.Category{Name: name, TaxID: f.taxID, ParentID: parentID})
	if err != nil {
		t.Fatalf("create %q: %v", name, err)
	}
	return c
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error: got %v, want %v", got, want)
	}
}

func TestCategoryStore_CreateAndGet(t *testing.T) {
	f := newCategoryFixture(t)

	root := f.create(t, "Limpieza test", nil)
	if root.ParentID != nil || root.ParentName != "" {
		t.Fatalf("root: got parent %v %q, want none", root.ParentID, root.ParentName)
	}
	if !root.IsActive || root.TaxName != "IVA test store" {
		t.Fatalf("root: got active=%v tax=%q", root.IsActive, root.TaxName)
	}

	child := f.create(t, "Jabones", &root.ID)
	got, err := f.store.GetByID(f.ctx, child.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ParentID == nil || *got.ParentID != root.ID || got.ParentName != "Limpieza test" {
		t.Fatalf("child: got parent %v %q, want %d %q", got.ParentID, got.ParentName, root.ID, "Limpieza test")
	}
}

func TestCategoryStore_GetByIDNotFound(t *testing.T) {
	f := newCategoryFixture(t)
	_, err := f.store.GetByID(f.ctx, -1)
	wantErr(t, err, store.ErrNotFound)
}

func TestCategoryStore_SameNameUnderDifferentParents(t *testing.T) {
	f := newCategoryFixture(t)
	a := f.create(t, "Limpieza test", nil)
	b := f.create(t, "Almacen test", nil)
	f.create(t, "Varios", &a.ID)
	f.create(t, "Varios", &b.ID) // no tiene que fallar
}

func TestCategoryStore_DuplicateSiblingIgnoresCase(t *testing.T) {
	f := newCategoryFixture(t)
	root := f.create(t, "Limpieza test", nil)
	f.create(t, "Jabones", &root.ID)

	_, err := f.store.Create(f.ctx, &model.Category{Name: "JABONES", TaxID: f.taxID, ParentID: &root.ID})
	wantErr(t, err, store.ErrDuplicateName)
}

func TestCategoryStore_DuplicateRoot(t *testing.T) {
	f := newCategoryFixture(t)
	f.create(t, "Limpieza test", nil)

	_, err := f.store.Create(f.ctx, &model.Category{Name: "limpieza TEST", TaxID: f.taxID})
	wantErr(t, err, store.ErrDuplicateName)
}

func TestCategoryStore_CreateMissingTax(t *testing.T) {
	f := newCategoryFixture(t)
	_, err := f.store.Create(f.ctx, &model.Category{Name: "Sin IVA", TaxID: 999999999})
	wantErr(t, err, store.ErrInvalidReference)
}

func TestCategoryStore_CreateMissingParent(t *testing.T) {
	f := newCategoryFixture(t)
	_, err := f.store.Create(f.ctx, &model.Category{Name: "Huerfana", TaxID: f.taxID, ParentID: new(int64(999999999))})
	wantErr(t, err, store.ErrInvalidReference)
}

func TestCategoryStore_CreateUnderInactiveParent(t *testing.T) {
	f := newCategoryFixture(t)
	root := f.create(t, "Limpieza test", nil)
	if err := f.store.SetActive(f.ctx, root.ID, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	_, err := f.store.Create(f.ctx, &model.Category{Name: "Jabones", TaxID: f.taxID, ParentID: &root.ID})
	wantErr(t, err, store.ErrCategoryParentInactive)
}

func TestCategoryStore_Update(t *testing.T) {
	f := newCategoryFixture(t)
	a := f.create(t, "Limpieza test", nil)
	b := f.create(t, "Perfumeria test", nil)
	child := f.create(t, "Jabones", &a.ID)

	// Mover Jabones de Limpieza a Perfumería y cambiarle el nombre.
	updated, err := f.store.Update(f.ctx, child.ID, &model.Category{Name: "Jabones de tocador", TaxID: f.taxID, ParentID: &b.ID})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "Jabones de tocador" || updated.ParentName != "Perfumeria test" {
		t.Fatalf("got %q under %q", updated.Name, updated.ParentName)
	}

	// Pasarla a raíz.
	updated, err = f.store.Update(f.ctx, child.ID, &model.Category{Name: "Jabones de tocador", TaxID: f.taxID})
	if err != nil {
		t.Fatalf("update to root: %v", err)
	}
	if updated.ParentID != nil {
		t.Fatalf("got parent %v, want nil", *updated.ParentID)
	}
}

func TestCategoryStore_UpdateNotFound(t *testing.T) {
	f := newCategoryFixture(t)
	_, err := f.store.Update(f.ctx, -1, &model.Category{Name: "Nada", TaxID: f.taxID})
	wantErr(t, err, store.ErrNotFound)
}

func TestCategoryStore_UpdateIgnoresIsActive(t *testing.T) {
	f := newCategoryFixture(t)
	c := f.create(t, "Limpieza test", nil)

	// IsActive en false (el valor cero) no tiene que desactivarla:
	// eso pasa solo por SetActive.
	updated, err := f.store.Update(f.ctx, c.ID, &model.Category{Name: "Limpieza test", TaxID: f.taxID, IsActive: false})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !updated.IsActive {
		t.Fatal("update deactivated the category")
	}
}

func TestCategoryStore_UpdateCycle(t *testing.T) {
	f := newCategoryFixture(t)
	// Limpieza > Jabones > En polvo
	a := f.create(t, "Limpieza test", nil)
	b := f.create(t, "Jabones", &a.ID)
	c := f.create(t, "En polvo", &b.ID)

	tests := []struct {
		name   string
		id     int64
		parent int64
	}{
		{"own parent", a.ID, a.ID},
		{"under its child", a.ID, b.ID},
		{"under its grandchild", a.ID, c.ID},
		{"middle under its child", b.ID, c.ID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.store.Update(f.ctx, tt.id, &model.Category{Name: "Movida", TaxID: f.taxID, ParentID: &tt.parent})
			wantErr(t, err, store.ErrCategoryCycle)
		})
	}
}

func TestCategoryStore_SetActiveTreeRules(t *testing.T) {
	f := newCategoryFixture(t)
	root := f.create(t, "Limpieza test", nil)
	child := f.create(t, "Jabones", &root.ID)

	// Con una hija activa, el padre no se puede desactivar.
	wantErr(t, f.store.SetActive(f.ctx, root.ID, false), store.ErrCategoryHasActiveChildren)

	// Primero la hija, después el padre.
	if err := f.store.SetActive(f.ctx, child.ID, false); err != nil {
		t.Fatalf("deactivate child: %v", err)
	}
	if err := f.store.SetActive(f.ctx, root.ID, false); err != nil {
		t.Fatalf("deactivate root: %v", err)
	}

	// Con el padre inactivo, la hija no se puede reactivar.
	wantErr(t, f.store.SetActive(f.ctx, child.ID, true), store.ErrCategoryParentInactive)

	// Al revés: primero el padre, después la hija.
	if err := f.store.SetActive(f.ctx, root.ID, true); err != nil {
		t.Fatalf("reactivate root: %v", err)
	}
	if err := f.store.SetActive(f.ctx, child.ID, true); err != nil {
		t.Fatalf("reactivate child: %v", err)
	}
}

func TestCategoryStore_SetActiveNotFound(t *testing.T) {
	f := newCategoryFixture(t)
	wantErr(t, f.store.SetActive(f.ctx, -1, false), store.ErrNotFound)
	wantErr(t, f.store.SetActive(f.ctx, -1, true), store.ErrNotFound)
}

func TestCategoryStore_Lists(t *testing.T) {
	f := newCategoryFixture(t)
	active := f.create(t, "Zzactiva test", nil)
	inactive := f.create(t, "Zzinactiva test", nil)
	if err := f.store.SetActive(f.ctx, inactive.ID, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	// La DB tiene además las categorías del seed: se busca por id, sin contar.
	contains := func(list []*model.Category, id int64) bool {
		for _, c := range list {
			if c.ID == id {
				return true
			}
		}
		return false
	}

	tests := []struct {
		name         string
		list         func() ([]*model.Category, error)
		wantActive   bool
		wantInactive bool
	}{
		{"GetAll", func() ([]*model.Category, error) { return f.store.GetAll(f.ctx) }, true, false},
		{"GetAllIncludingInactive", func() ([]*model.Category, error) { return f.store.GetAllIncludingInactive(f.ctx) }, true, true},
		{"SearchByName", func() ([]*model.Category, error) { return f.store.SearchByName(f.ctx, "zz") }, true, false},
		{"SearchAllByName", func() ([]*model.Category, error) { return f.store.SearchAllByName(f.ctx, "ZZ") }, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			list, err := tt.list()
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if got := contains(list, active.ID); got != tt.wantActive {
				t.Errorf("active in list: got %v, want %v", got, tt.wantActive)
			}
			if got := contains(list, inactive.ID); got != tt.wantInactive {
				t.Errorf("inactive in list: got %v, want %v", got, tt.wantInactive)
			}
		})
	}
}

func TestCategoryStore_Delete(t *testing.T) {
	f := newCategoryFixture(t)
	c := f.create(t, "Limpieza test", nil)

	if err := f.store.Delete(f.ctx, c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	wantErr(t, f.store.Delete(f.ctx, c.ID), store.ErrNotFound)
}

func TestCategoryStore_DeleteWithChildren(t *testing.T) {
	f := newCategoryFixture(t)
	root := f.create(t, "Limpieza test", nil)
	f.create(t, "Jabones", &root.ID)

	wantErr(t, f.store.Delete(f.ctx, root.ID), store.ErrInUse)
}
