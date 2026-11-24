package createproduct

// Test interno (mismo paquete): necesita cambiar s.codes para simular que el
// INSERT del código falla después de crear el producto.

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
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/productcodes"
)

// fixture: una transacción que se deshace al terminar, una categoría, y el
// caso de uso con SavepointRunner (no se puede abrir otra transacción
// adentro de la del test).
type fixture struct {
	ctx        context.Context
	tx         *sql.Tx
	svc        *Service
	taxID      int64
	categoryID int64
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

	f := &fixture{ctx: context.Background(), tx: tx, svc: New(postgres.NewSavepointRunner(tx))}
	if err := tx.QueryRowContext(f.ctx,
		`INSERT INTO taxes (name, rate) VALUES ('IVA test create', 21) RETURNING id;`).Scan(&f.taxID); err != nil {
		t.Fatalf("fixture tax: %v", err)
	}
	if err := tx.QueryRowContext(f.ctx,
		`INSERT INTO categories (name, tax_id) VALUES ('Categoria test create', $1) RETURNING id;`,
		f.taxID).Scan(&f.categoryID); err != nil {
		t.Fatalf("fixture category: %v", err)
	}
	return f
}

func (f *fixture) product(name string) *model.Product {
	return &model.Product{Name: name, CategoryID: f.categoryID}
}

// count cuenta filas para ver qué quedó guardado.
func (f *fixture) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := f.tx.QueryRowContext(f.ctx, q, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", q, err)
	}
	return n
}

func (f *fixture) productsNamed(t *testing.T, name string) int {
	return f.count(t, `SELECT count(*) FROM products WHERE name = $1;`, name)
}

func wantErr(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error: got %v, want %v", got, want)
	}
}

// Los códigos de prueba empiezan con 0000000977: no chocan con el seed ni
// con el catálogo real.

func TestCreateWithoutCode(t *testing.T) {
	f := newFixture(t)

	p, err := f.svc.Create(f.ctx, Request{Product: f.product("sin codigo test"), Barcode: "   "})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.ID == 0 || p.SKU == "" || p.Name != "SIN CODIGO TEST" {
		t.Errorf("create: got %+v", p)
	}
	if n := f.count(t, `SELECT count(*) FROM product_codes WHERE product_id = $1;`, p.ID); n != 0 {
		t.Errorf("codes: %d, want 0", n)
	}
}

func TestCreateWithCode(t *testing.T) {
	f := newFixture(t)

	p, err := f.svc.Create(f.ctx, Request{Product: f.product("CON CODIGO TEST"), Barcode: " 9770201 "})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	var code, codeType string
	err = f.tx.QueryRowContext(f.ctx,
		`SELECT code, code_type FROM product_codes WHERE product_id = $1;`, p.ID).Scan(&code, &codeType)
	if err != nil {
		t.Fatalf("read code: %v", err)
	}
	if code != "0000009770201" || codeType != string(model.CodeTypeInternal) {
		t.Errorf("code: got %q/%s", code, codeType)
	}
}

func TestCreateInvalidProductChecksNothingElse(t *testing.T) {
	f := newFixture(t)

	// El producto se valida primero: el código mal escrito ni se mira.
	_, err := f.svc.Create(f.ctx, Request{Product: &model.Product{Name: "X", CategoryID: f.categoryID}, Barcode: "abc"})
	wantErr(t, err, service.ErrValidation)
	wantErr(t, err, model.ErrProductNameTooShort)
}

func TestCreateInvalidCode(t *testing.T) {
	f := newFixture(t)

	_, err := f.svc.Create(f.ctx, Request{Product: f.product("CODIGO MALO TEST"), Barcode: "77-90"})
	wantErr(t, err, service.ErrValidation)
	wantErr(t, err, model.ErrBarcodeNotDigits)
	if n := f.productsNamed(t, "CODIGO MALO TEST"); n != 0 {
		t.Errorf("products created: %d, want 0", n)
	}
}

func TestCreateTakenCode(t *testing.T) {
	f := newFixture(t)
	owner, err := f.svc.Create(f.ctx, Request{Product: f.product("DUEÑO TEST"), Barcode: "9770202"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}

	_, err = f.svc.Create(f.ctx, Request{Product: f.product("SEGUNDO TEST"), Barcode: "0000009770202"})
	wantErr(t, err, service.ErrAlreadyExists)
	var taken *productcodes.CodeTakenError
	if !errors.As(err, &taken) || taken.ProductID != owner.ID || taken.ProductName != "DUEÑO TEST" {
		t.Fatalf("got %v, want CodeTakenError for %d", err, owner.ID)
	}
	if n := f.productsNamed(t, "SEGUNDO TEST"); n != 0 {
		t.Errorf("products created: %d, want 0", n)
	}
}

// failingCodes simula que el INSERT del código falla (ej. la DB se cae)
// cuando el producto ya se creó en la misma transacción.
type failingCodes struct{ codeStore }

var errCodeInsert = errors.New("code insert failed")

func (failingCodes) Add(context.Context, *model.ProductCode) (*model.ProductCode, error) {
	return nil, errCodeInsert
}

func TestCreateRollsBackProductWhenCodeFails(t *testing.T) {
	f := newFixture(t)
	f.svc.codes = func(tx postgres.DBTX) codeStore {
		return failingCodes{postgres.NewProductCodeStore(tx)}
	}

	_, err := f.svc.Create(f.ctx, Request{Product: f.product("A MEDIAS TEST"), Barcode: "9770203"})
	wantErr(t, err, errCodeInsert)
	if n := f.productsNamed(t, "A MEDIAS TEST"); n != 0 {
		t.Errorf("products left after rollback: %d, want 0", n)
	}
}

// ---- Marca y categoría nuevas en el alta ----

func (f *fixture) brandsNamed(t *testing.T, name string) int {
	return f.count(t, `SELECT count(*) FROM brands WHERE name = $1;`, name)
}

func (f *fixture) categoriesNamed(t *testing.T, name string) int {
	return f.count(t, `SELECT count(*) FROM categories WHERE name = $1;`, name)
}

func TestCreateWithNewBrand(t *testing.T) {
	f := newFixture(t)

	p, err := f.svc.Create(f.ctx, Request{Product: f.product("CON MARCA NUEVA TEST"), NewBrand: "  marca   nueva test "})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.BrandID == nil || p.BrandName != "MARCA NUEVA TEST" {
		t.Errorf("brand: got %v %q, want MARCA NUEVA TEST", p.BrandID, p.BrandName)
	}
}

// Si la marca ya existe (otro la creó mientras tanto), se usa esa: no se
// crea otra igual ni falla por nombre repetido.
func TestCreateWithNewBrandThatExists(t *testing.T) {
	f := newFixture(t)
	var brandID int64
	if err := f.tx.QueryRowContext(f.ctx,
		`INSERT INTO brands (name) VALUES ('MARCA EXISTE TEST') RETURNING id;`).Scan(&brandID); err != nil {
		t.Fatalf("insert brand: %v", err)
	}

	p, err := f.svc.Create(f.ctx, Request{Product: f.product("MARCA REUSADA TEST"), NewBrand: "marca existe test"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.BrandID == nil || *p.BrandID != brandID {
		t.Errorf("brand: got %v, want %d", p.BrandID, brandID)
	}
	if n := f.brandsNamed(t, "MARCA EXISTE TEST"); n != 1 {
		t.Errorf("brands: %d, want 1", n)
	}
}

// Una marca desactivada no se reactiva sola: se avisa y no se crea nada.
func TestCreateWithNewBrandThatIsInactive(t *testing.T) {
	f := newFixture(t)
	if _, err := f.tx.ExecContext(f.ctx,
		`INSERT INTO brands (name, is_active) VALUES ('MARCA APAGADA TEST', FALSE);`); err != nil {
		t.Fatalf("insert brand: %v", err)
	}

	_, err := f.svc.Create(f.ctx, Request{Product: f.product("CON MARCA APAGADA TEST"), NewBrand: "MARCA APAGADA TEST"})
	wantErr(t, err, service.ErrConflict)
	var inactive *BrandInactiveError
	if !errors.As(err, &inactive) || inactive.Name != "MARCA APAGADA TEST" {
		t.Fatalf("got %v, want BrandInactiveError", err)
	}
	if n := f.productsNamed(t, "CON MARCA APAGADA TEST"); n != 0 {
		t.Errorf("products created: %d, want 0", n)
	}
}

func TestCreateWithNewCategory(t *testing.T) {
	f := newFixture(t)
	parent := f.categoryID

	p, err := f.svc.Create(f.ctx, Request{
		Product:     f.product("CON CATEGORIA NUEVA TEST"),
		NewCategory: &model.Category{Name: "Subcategoria test", ParentID: &parent, TaxID: f.taxID},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.CategoryID == f.categoryID || p.CategoryName != "Subcategoria test" {
		t.Errorf("category: got %d %q", p.CategoryID, p.CategoryName)
	}
	var gotParent int64
	if err := f.tx.QueryRowContext(f.ctx, `SELECT parent_id FROM categories WHERE id = $1;`, p.CategoryID).Scan(&gotParent); err != nil {
		t.Fatalf("read category: %v", err)
	}
	if gotParent != parent {
		t.Errorf("parent: got %d, want %d", gotParent, parent)
	}
}

func TestCreateWithInvalidNewCategory(t *testing.T) {
	f := newFixture(t)

	// Sin impuesto: la categoría define el IVA, es obligatorio.
	_, err := f.svc.Create(f.ctx, Request{
		Product:     f.product("CATEGORIA SIN IVA TEST"),
		NewCategory: &model.Category{Name: "Sin iva test"},
	})
	wantErr(t, err, service.ErrValidation)
	wantErr(t, err, model.ErrCategoryTaxRequired)
	if n := f.categoriesNamed(t, "Sin iva test"); n != 0 {
		t.Errorf("categories created: %d, want 0", n)
	}
}

// Una categoría con el mismo nombre en el mismo lugar del árbol ya existe:
// hay que elegirla de la lista. Va al final: el error aborta la transacción.
func TestCreateWithNewCategoryThatExists(t *testing.T) {
	f := newFixture(t)

	_, err := f.svc.Create(f.ctx, Request{
		Product:     f.product("CATEGORIA REPETIDA TEST"),
		NewCategory: &model.Category{Name: "Categoria test create", TaxID: f.taxID},
	})
	wantErr(t, err, service.ErrAlreadyExists)
}

// Si algo falla al final (acá, guardar el código), no queda nada: ni el
// producto, ni la marca, ni la categoría. Y lo que mandó quien llama no
// cambia: el formulario vuelve con lo escrito.
func TestCreateRollsBackNewBrandAndCategory(t *testing.T) {
	f := newFixture(t)
	f.svc.codes = func(tx postgres.DBTX) codeStore {
		return failingCodes{postgres.NewProductCodeStore(tx)}
	}
	product := f.product("TODO A MEDIAS TEST")
	product.CategoryID = 0

	_, err := f.svc.Create(f.ctx, Request{
		Product:     product,
		Barcode:     "9770204",
		NewBrand:    "MARCA A MEDIAS TEST",
		NewCategory: &model.Category{Name: "Categoria a medias test", TaxID: f.taxID},
	})
	wantErr(t, err, errCodeInsert)
	if n := f.productsNamed(t, "TODO A MEDIAS TEST"); n != 0 {
		t.Errorf("products: %d, want 0", n)
	}
	if n := f.brandsNamed(t, "MARCA A MEDIAS TEST"); n != 0 {
		t.Errorf("brands: %d, want 0", n)
	}
	if n := f.categoriesNamed(t, "Categoria a medias test"); n != 0 {
		t.Errorf("categories: %d, want 0", n)
	}
	if product.BrandID != nil || product.CategoryID != 0 {
		t.Errorf("request product changed: brand %v, category %d", product.BrandID, product.CategoryID)
	}
}
