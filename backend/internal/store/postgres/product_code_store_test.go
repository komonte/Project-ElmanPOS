package postgres_test

import (
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Los códigos de prueba empiezan con 0000000977 para no chocar con el seed
// ni con el catálogo real (todos sus EAN13 empiezan con 7).

// code arma un código válido y normalizado para un producto.
func code(productID int64, raw string) *model.ProductCode {
	c := &model.ProductCode{ProductID: productID, Code: raw}
	if err := c.Validate(); err != nil {
		panic(err) // un dato de prueba inválido es un error del test
	}
	return c
}

func TestProductCodeStore_AddAndList(t *testing.T) {
	f := newProductFixture(t)
	codes := postgres.NewProductCodeStore(f.tx)
	p := f.create(t, "TEST-PC-1", "CODIGOS TEST")

	got, err := codes.Add(f.ctx, code(p.ID, "9770001"))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if got.ID == 0 || got.ProductID != p.ID || got.Code != "0000009770001" ||
		got.CodeType != model.CodeTypeInternal || got.CreatedAt == "" {
		t.Errorf("add: got %+v", got)
	}
	if got.ProductName != "CODIGOS TEST" || got.ProductSKU != "TEST-PC-1" {
		t.Errorf("add product: got %q/%q", got.ProductSKU, got.ProductName)
	}

	if _, err := codes.Add(f.ctx, code(p.ID, "9770002")); err != nil {
		t.Fatalf("add second: %v", err)
	}

	list, err := codes.ListByProduct(f.ctx, p.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].Code != "0000009770001" || list[1].Code != "0000009770002" {
		t.Errorf("list: got %d codes %+v", len(list), list)
	}
}

func TestProductCodeStore_ListEmpty(t *testing.T) {
	f := newProductFixture(t)
	codes := postgres.NewProductCodeStore(f.tx)
	p := f.create(t, "TEST-PC-2", "SIN CODIGOS TEST")

	list, err := codes.ListByProduct(f.ctx, p.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if list == nil || len(list) != 0 {
		t.Errorf("list: got %#v, want empty non-nil", list)
	}
}

func TestProductCodeStore_FindByCode(t *testing.T) {
	f := newProductFixture(t)
	codes := postgres.NewProductCodeStore(f.tx)
	p := f.create(t, "TEST-PC-3", "BUSCAR CODIGO TEST")
	if _, err := codes.Add(f.ctx, code(p.ID, "9770003")); err != nil {
		t.Fatalf("add: %v", err)
	}

	got, err := codes.FindByCode(f.ctx, "0000009770003")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.ProductID != p.ID || got.ProductName != "BUSCAR CODIGO TEST" {
		t.Errorf("find: got %+v", got)
	}

	_, err = codes.FindByCode(f.ctx, "0000009779999")
	wantErr(t, err, store.ErrNotFound)
}

// Un código es de un solo producto: ni otro producto ni el mismo pueden
// cargarlo de nuevo. Va al final: el error deja la transacción abortada.
func TestProductCodeStore_AddDuplicate(t *testing.T) {
	f := newProductFixture(t)
	codes := postgres.NewProductCodeStore(f.tx)
	a := f.create(t, "TEST-PC-4", "DUEÑO TEST")
	b := f.create(t, "TEST-PC-5", "OTRO TEST")
	if _, err := codes.Add(f.ctx, code(a.ID, "9770004")); err != nil {
		t.Fatalf("add: %v", err)
	}

	_, err := codes.Add(f.ctx, code(b.ID, "9770004"))
	wantErr(t, err, store.ErrDuplicateCode)
	wantErr(t, err, store.ErrDuplicate) // el service genérico pregunta por este
}

func TestProductCodeStore_AddMissingProduct(t *testing.T) {
	f := newProductFixture(t)
	codes := postgres.NewProductCodeStore(f.tx)

	_, err := codes.Add(f.ctx, code(999999999, "9770005"))
	wantErr(t, err, store.ErrInvalidReference)
}

func TestProductCodeStore_Delete(t *testing.T) {
	f := newProductFixture(t)
	codes := postgres.NewProductCodeStore(f.tx)
	a := f.create(t, "TEST-PC-6", "BORRAR CODIGO TEST")
	b := f.create(t, "TEST-PC-7", "AJENO TEST")
	c, err := codes.Add(f.ctx, code(a.ID, "9770006"))
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Con otro producto en la URL no se borra: el código no es suyo.
	wantErr(t, codes.Delete(f.ctx, b.ID, c.ID), store.ErrNotFound)

	if err := codes.Delete(f.ctx, a.ID, c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	wantErr(t, codes.Delete(f.ctx, a.ID, c.ID), store.ErrNotFound)

	list, err := codes.ListByProduct(f.ctx, a.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("codes left: %d", len(list))
	}
}
