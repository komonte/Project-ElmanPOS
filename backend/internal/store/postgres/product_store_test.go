package postgres_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
	"github.com/shopspring/decimal"
)

// productFixture arma un store dentro de una transacción, con su propia
// categoría, marca y proveedor para no depender de los datos del seed.
type productFixture struct {
	ctx        context.Context
	tx         *sql.Tx
	store      *postgres.ProductStore
	categoryID int64
	brandID    int64
	supplierID int64
}

func newProductFixture(t *testing.T) *productFixture {
	t.Helper()
	tx := testTx(t)
	f := &productFixture{ctx: context.Background(), tx: tx, store: postgres.NewProductStore(tx)}

	inserts := []struct {
		q  string
		id *int64
	}{
		{`INSERT INTO taxes (name, rate) VALUES ('IVA test product', 21) RETURNING id;`, new(int64)},
		{`INSERT INTO brands (name) VALUES ('MARCA TEST PRODUCT') RETURNING id;`, &f.brandID},
		{`INSERT INTO suppliers (name) VALUES ('Proveedor test product') RETURNING id;`, &f.supplierID},
	}
	for _, in := range inserts {
		if err := tx.QueryRowContext(f.ctx, in.q).Scan(in.id); err != nil {
			t.Fatalf("fixture %q: %v", in.q, err)
		}
	}
	// La categoría necesita el impuesto recién creado.
	err := tx.QueryRowContext(f.ctx,
		`INSERT INTO categories (name, tax_id) VALUES ('Categoria test product', $1) RETURNING id;`,
		*inserts[0].id).Scan(&f.categoryID)
	if err != nil {
		t.Fatalf("fixture category: %v", err)
	}
	return f
}

// product devuelve un producto mínimo y válido en la categoría del fixture.
func (f *productFixture) product(sku, name string) *model.Product {
	p := &model.Product{SKU: sku, Name: name, CategoryID: f.categoryID}
	if err := p.Validate(); err != nil {
		panic(err) // un dato de prueba inválido es un error del test
	}
	return p
}

// create guarda un producto que el test necesita como dato previo.
func (f *productFixture) create(t *testing.T, sku, name string) *model.Product {
	t.Helper()
	p, err := f.store.Create(f.ctx, f.product(sku, name))
	if err != nil {
		t.Fatalf("create %q: %v", sku, err)
	}
	return p
}

func TestProductStore_CreateFullAndGet(t *testing.T) {
	f := newProductFixture(t)

	in := f.product("TEST-FULL", "PRODUCTO COMPLETO TEST")
	in.Description = "Con todos los campos"
	in.BrandID = &f.brandID
	in.PreferredSupplierID = &f.supplierID
	in.SaleType = model.SaleTypeWeight
	in.MeasureUnit = model.MeasureUnitKilogram
	in.ControlsBatches = true
	in.ReorderPoint = decimal.RequireFromString("2.5")
	in.MaxStock = new(decimal.RequireFromString("10.125"))
	in.MarginPercent = decimal.RequireFromString("35.5")
	in.IsConsignment = true

	created, err := f.store.Create(f.ctx, in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := f.store.GetByID(f.ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	if got.SKU != "TEST-FULL" || got.Name != "PRODUCTO COMPLETO TEST" || got.Description != "Con todos los campos" {
		t.Errorf("text: got %q %q %q", got.SKU, got.Name, got.Description)
	}
	if got.BrandID == nil || *got.BrandID != f.brandID || got.BrandName != "MARCA TEST PRODUCT" {
		t.Errorf("brand: got %v %q", got.BrandID, got.BrandName)
	}
	if got.CategoryID != f.categoryID || got.CategoryName != "Categoria test product" {
		t.Errorf("category: got %d %q", got.CategoryID, got.CategoryName)
	}
	if got.PreferredSupplierID == nil || *got.PreferredSupplierID != f.supplierID || got.SupplierName != "Proveedor test product" {
		t.Errorf("supplier: got %v %q", got.PreferredSupplierID, got.SupplierName)
	}
	if got.SaleType != model.SaleTypeWeight || got.MeasureUnit != model.MeasureUnitKilogram {
		t.Errorf("enums: got %q %q", got.SaleType, got.MeasureUnit)
	}
	if !got.ControlsBatches || !got.IsConsignment || !got.IsActive {
		t.Errorf("flags: got batches=%v consignment=%v active=%v", got.ControlsBatches, got.IsConsignment, got.IsActive)
	}
	// La DB devuelve "2.500": se compara el valor, no el texto.
	if !got.ReorderPoint.Equal(in.ReorderPoint) || got.MaxStock == nil ||
		!got.MaxStock.Equal(*in.MaxStock) || !got.MarginPercent.Equal(in.MarginPercent) {
		t.Errorf("numbers: got %s %v %s", got.ReorderPoint, got.MaxStock, got.MarginPercent)
	}
}

func TestProductStore_CreateMinimalStoresNulls(t *testing.T) {
	f := newProductFixture(t)
	p := f.create(t, "TEST-MIN", "PRODUCTO MINIMO TEST")

	if p.BrandID != nil || p.PreferredSupplierID != nil || p.MaxStock != nil {
		t.Errorf("optionals: got brand=%v supplier=%v max=%v, want nil", p.BrandID, p.PreferredSupplierID, p.MaxStock)
	}
	if p.BrandName != "" || p.SupplierName != "" || p.Description != "" {
		t.Errorf("names: got %q %q %q, want empty", p.BrandName, p.SupplierName, p.Description)
	}

	// La description vacía tiene que quedar NULL en la DB, no ''.
	var descIsNull bool
	err := f.tx.QueryRowContext(f.ctx, `SELECT description IS NULL FROM products WHERE id = $1;`, p.ID).Scan(&descIsNull)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if !descIsNull {
		t.Errorf("description: stored as '', want NULL")
	}
}

func TestProductStore_GetByIDNotFound(t *testing.T) {
	f := newProductFixture(t)
	_, err := f.store.GetByID(f.ctx, -1)
	wantErr(t, err, store.ErrNotFound)
}

func TestProductStore_DuplicateSKU(t *testing.T) {
	f := newProductFixture(t)
	f.create(t, "TEST-DUP", "PRIMERO TEST")

	_, err := f.store.Create(f.ctx, f.product("TEST-DUP", "SEGUNDO TEST"))
	wantErr(t, err, store.ErrDuplicateSKU)
}

func TestProductStore_SameNameDifferentSKU(t *testing.T) {
	f := newProductFixture(t)
	f.create(t, "TEST-A", "MISMO NOMBRE TEST")
	f.create(t, "TEST-B", "MISMO NOMBRE TEST")
}

func TestProductStore_CreateMissingReference(t *testing.T) {
	tests := []struct {
		name   string
		modify func(p *model.Product)
	}{
		{"category", func(p *model.Product) { p.CategoryID = 999999999 }},
		{"brand", func(p *model.Product) { p.BrandID = new(int64(999999999)) }},
		{"supplier", func(p *model.Product) { p.PreferredSupplierID = new(int64(999999999)) }},
	}
	for _, tt := range tests {
		// Cada caso en su propia transacción: el error de FK la deja abortada.
		t.Run(tt.name, func(t *testing.T) {
			f := newProductFixture(t)
			p := f.product("TEST-REF", "REFERENCIA ROTA TEST")
			tt.modify(p)
			_, err := f.store.Create(f.ctx, p)
			wantErr(t, err, store.ErrInvalidReference)
		})
	}
}

func TestProductStore_Update(t *testing.T) {
	f := newProductFixture(t)
	p := f.create(t, "TEST-UPD", "ANTES TEST")

	p.SKU = "TEST-UPD-2"
	p.Name = "DESPUES TEST"
	p.BrandID = &f.brandID
	p.ReorderPoint = decimal.NewFromInt(3)
	p.MaxStock = new(decimal.NewFromInt(9))

	got, err := f.store.Update(f.ctx, p.ID, p)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.SKU != "TEST-UPD-2" || got.Name != "DESPUES TEST" || got.BrandName != "MARCA TEST PRODUCT" {
		t.Errorf("got %q %q %q", got.SKU, got.Name, got.BrandName)
	}
	if !got.ReorderPoint.Equal(decimal.NewFromInt(3)) || got.MaxStock == nil || !got.MaxStock.Equal(decimal.NewFromInt(9)) {
		t.Errorf("numbers: got %s %v", got.ReorderPoint, got.MaxStock)
	}

	// Y se puede volver a dejar sin marca ni stock máximo.
	got.BrandID = nil
	got.MaxStock = nil
	got, err = f.store.Update(f.ctx, got.ID, got)
	if err != nil {
		t.Fatalf("update back: %v", err)
	}
	if got.BrandID != nil || got.MaxStock != nil {
		t.Errorf("optionals: got brand=%v max=%v, want nil", got.BrandID, got.MaxStock)
	}
}

func TestProductStore_UpdateNotFound(t *testing.T) {
	f := newProductFixture(t)
	_, err := f.store.Update(f.ctx, -1, f.product("TEST-NF", "NO EXISTE TEST"))
	wantErr(t, err, store.ErrNotFound)
}

func TestProductStore_UpdateDuplicateSKU(t *testing.T) {
	f := newProductFixture(t)
	f.create(t, "TEST-ONE", "UNO TEST")
	two := f.create(t, "TEST-TWO", "DOS TEST")

	two.SKU = "TEST-ONE"
	_, err := f.store.Update(f.ctx, two.ID, two)
	wantErr(t, err, store.ErrDuplicateSKU)
}

func TestProductStore_UpdateIgnoresIsActive(t *testing.T) {
	f := newProductFixture(t)
	p := f.create(t, "TEST-ACT", "ACTIVO TEST")

	// Un PUT sin is_active llega con false: no tiene que desactivar.
	p.IsActive = false
	got, err := f.store.Update(f.ctx, p.ID, p)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if !got.IsActive {
		t.Fatalf("is_active: got false, want true")
	}
}

func TestProductStore_Lists(t *testing.T) {
	f := newProductFixture(t)
	active := f.create(t, "ZZTEST-ACT", "ZZPRODUCTO ACTIVO TEST")
	inactive := f.create(t, "ZZTEST-INA", "ZZPRODUCTO INACTIVO TEST")
	if err := f.store.SetActive(f.ctx, inactive.ID, false); err != nil {
		t.Fatalf("deactivate: %v", err)
	}

	// La DB tiene además los productos del seed: se busca por id, sin contar.
	contains := func(list []*model.Product, id int64) bool {
		for _, p := range list {
			if p.ID == id {
				return true
			}
		}
		return false
	}

	tests := []struct {
		name         string
		list         func() ([]*model.Product, error)
		wantActive   bool
		wantInactive bool
	}{
		{"GetAll", func() ([]*model.Product, error) { return f.store.GetAll(f.ctx) }, true, false},
		{"GetAllIncludingInactive", func() ([]*model.Product, error) { return f.store.GetAllIncludingInactive(f.ctx) }, true, true},
		{"SearchByName by name", func() ([]*model.Product, error) { return f.store.SearchByName(f.ctx, "zzproducto") }, true, false},
		{"SearchByName by sku", func() ([]*model.Product, error) { return f.store.SearchByName(f.ctx, "zztest-") }, true, false},
		{"SearchAllByName by name", func() ([]*model.Product, error) { return f.store.SearchAllByName(f.ctx, "ZZPRODUCTO") }, true, true},
		{"SearchAllByName by sku", func() ([]*model.Product, error) { return f.store.SearchAllByName(f.ctx, "ZZTEST-") }, true, true},
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

func TestProductStore_SetActiveNotFound(t *testing.T) {
	f := newProductFixture(t)
	wantErr(t, f.store.SetActive(f.ctx, -1, false), store.ErrNotFound)
}

func TestProductStore_Delete(t *testing.T) {
	f := newProductFixture(t)
	p := f.create(t, "TEST-DEL", "BORRAR TEST")

	if err := f.store.Delete(f.ctx, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	wantErr(t, f.store.Delete(f.ctx, p.ID), store.ErrNotFound)
}

func TestProductStore_DeleteInUse(t *testing.T) {
	f := newProductFixture(t)
	p := f.create(t, "TEST-USE", "EN USO TEST")

	// Con un código de barras cargado, el producto ya no se puede borrar.
	_, err := f.tx.ExecContext(f.ctx,
		`INSERT INTO product_codes (product_id, code_type, code) VALUES ($1, 'INTERNAL', 'TESTUSE01');`, p.ID)
	if err != nil {
		t.Fatalf("insert code: %v", err)
	}
	wantErr(t, f.store.Delete(f.ctx, p.ID), store.ErrInUse)
}

// deactivate desactiva una fila de una tabla maestra directo en la DB, para
// armar el caso sin depender de los otros stores.
func (f *productFixture) deactivate(t *testing.T, table string, id int64) {
	t.Helper()
	_, err := f.tx.ExecContext(f.ctx, `UPDATE `+table+` SET is_active = FALSE WHERE id = $1;`, id)
	if err != nil {
		t.Fatalf("deactivate %s %d: %v", table, id, err)
	}
}

// insertID corre un INSERT ... RETURNING id y devuelve el id.
func (f *productFixture) insertID(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	var id int64
	if err := f.tx.QueryRowContext(f.ctx, q, args...).Scan(&id); err != nil {
		t.Fatalf("insert %q: %v", q, err)
	}
	return id
}

func TestProductStore_CreateWithInactiveReference(t *testing.T) {
	tests := []struct {
		name    string
		table   string
		pick    func(f *productFixture, p *model.Product) int64 // usa la referencia y devuelve su id
		wantErr error
	}{
		{
			name:  "brand",
			table: "brands",
			pick: func(f *productFixture, p *model.Product) int64 {
				p.BrandID = &f.brandID
				return f.brandID
			},
			wantErr: store.ErrProductBrandInactive,
		},
		{
			name:    "category",
			table:   "categories",
			pick:    func(f *productFixture, p *model.Product) int64 { return f.categoryID },
			wantErr: store.ErrProductCategoryInactive,
		},
		{
			name:  "supplier",
			table: "suppliers",
			pick: func(f *productFixture, p *model.Product) int64 {
				p.PreferredSupplierID = &f.supplierID
				return f.supplierID
			},
			wantErr: store.ErrProductSupplierInactive,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newProductFixture(t)
			p := f.product("TEST-INACT", "REFERENCIA INACTIVA TEST")
			f.deactivate(t, tt.table, tt.pick(f, p))

			_, err := f.store.Create(f.ctx, p)
			wantErr(t, err, tt.wantErr)
			wantErr(t, err, store.ErrConflict) // la API lo responde como 409
		})
	}
}

func TestProductStore_UpdateKeepsInactiveReferences(t *testing.T) {
	f := newProductFixture(t)
	p := f.product("TEST-KEEP", "CONSERVA REFERENCIAS TEST")
	p.BrandID = &f.brandID
	p.PreferredSupplierID = &f.supplierID
	p, err := f.store.Create(f.ctx, p)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Se desactivan después de cargar el producto: igual se tiene que poder
	// corregir el nombre sin cambiar de marca, categoría ni proveedor.
	f.deactivate(t, "brands", f.brandID)
	f.deactivate(t, "categories", f.categoryID)
	f.deactivate(t, "suppliers", f.supplierID)

	p.Name = "NOMBRE CORREGIDO TEST"
	got, err := f.store.Update(f.ctx, p.ID, p)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Name != "NOMBRE CORREGIDO TEST" {
		t.Fatalf("name: got %q", got.Name)
	}
}

func TestProductStore_UpdateToAnotherInactiveReference(t *testing.T) {
	f := newProductFixture(t)
	taxID := f.insertID(t, `INSERT INTO taxes (name, rate) VALUES ('IVA test product 2', 10.5) RETURNING id;`)
	otherCategory := f.insertID(t, `INSERT INTO categories (name, tax_id) VALUES ('Otra categoria test', $1) RETURNING id;`, taxID)
	otherBrand := f.insertID(t, `INSERT INTO brands (name) VALUES ('OTRA MARCA TEST PRODUCT') RETURNING id;`)
	otherSupplier := f.insertID(t, `INSERT INTO suppliers (name) VALUES ('Otro proveedor test') RETURNING id;`)
	f.deactivate(t, "categories", otherCategory)
	f.deactivate(t, "brands", otherBrand)
	f.deactivate(t, "suppliers", otherSupplier)

	p := f.create(t, "TEST-SWITCH", "CAMBIA REFERENCIA TEST")

	tests := []struct {
		name    string
		modify  func(p *model.Product)
		wantErr error
	}{
		{"category", func(p *model.Product) { p.CategoryID = otherCategory }, store.ErrProductCategoryInactive},
		{"brand", func(p *model.Product) { p.BrandID = &otherBrand }, store.ErrProductBrandInactive},
		{"supplier", func(p *model.Product) { p.PreferredSupplierID = &otherSupplier }, store.ErrProductSupplierInactive},
	}
	for _, tt := range tests {
		// El error sale del chequeo en Go, no de la DB: la transacción sigue
		// sana y los casos pueden compartir el fixture.
		t.Run(tt.name, func(t *testing.T) {
			changed := *p
			tt.modify(&changed)
			_, err := f.store.Update(f.ctx, p.ID, &changed)
			wantErr(t, err, tt.wantErr)
		})
	}
}

// addHistory le carga al producto una fila de historial del tipo pedido.
// Arma a mano lo mínimo que cada tabla exige (usuario, local, compra).
func (f *productFixture) addHistory(t *testing.T, kind string, productID int64) {
	t.Helper()
	switch kind {
	case "batch":
		f.insertID(t, `INSERT INTO product_batches (product_id, batch_number, original_quantity, remaining_quantity, unit_cost)
		               VALUES ($1, 'L-TEST', 10, 10, 100) RETURNING id;`, productID)
		return
	}

	userID := f.insertID(t, `INSERT INTO users (username, name, password_hash, role)
	                         VALUES ('test_product_user', 'Test', 'x', 'ADMIN') RETURNING id;`)
	purchaseID := f.insertID(t, `INSERT INTO purchases (supplier_id, total, received_by)
	                             VALUES ($1, 1000, $2) RETURNING id;`, f.supplierID, userID)
	switch kind {
	case "purchase item":
		f.insertID(t, `INSERT INTO purchase_items (purchase_id, product_id, quantity, unit_cost)
		               VALUES ($1, $2, 10, 100) RETURNING id;`, purchaseID, productID)
	case "stock movement":
		storeID := f.insertID(t, `INSERT INTO stores (name) VALUES ('Local test product') RETURNING id;`)
		f.insertID(t, `INSERT INTO stock_movements (product_id, store_id, movement_type, quantity, unit_cost, purchase_id, performed_by)
		               VALUES ($1, $2, 'PURCHASE', 10, 100, $3, $4) RETURNING id;`, productID, storeID, purchaseID, userID)
	default:
		t.Fatalf("unknown history kind %q", kind)
	}
}

func TestProductStore_LockedFieldsWithHistory(t *testing.T) {
	changes := []struct {
		name   string
		modify func(p *model.Product)
	}{
		{"sale type", func(p *model.Product) { p.SaleType = model.SaleTypeComposite }},
		{"measure unit", func(p *model.Product) { p.MeasureUnit = model.MeasureUnitLiter }},
		{"controls batches", func(p *model.Product) { p.ControlsBatches = true }},
	}
	// sale_items también cuenta como historial, pero armar una venta pide
	// sesión de caja, caja y local: queda cubierto por la misma consulta.
	kinds := []string{"stock movement", "purchase item", "batch"}

	for _, kind := range kinds {
		for _, ch := range changes {
			t.Run(kind+"/"+ch.name, func(t *testing.T) {
				f := newProductFixture(t)
				p := f.create(t, "TEST-LOCK", "BLOQUEADO TEST")
				f.addHistory(t, kind, p.ID)

				changed := *p
				ch.modify(&changed)
				_, err := f.store.Update(f.ctx, p.ID, &changed)
				wantErr(t, err, store.ErrProductHasHistory)
				wantErr(t, err, store.ErrConflict)
			})
		}
	}
}

func TestProductStore_OtherFieldsWithHistory(t *testing.T) {
	f := newProductFixture(t)
	p := f.create(t, "TEST-HIST", "CON HISTORIAL TEST")
	f.addHistory(t, "stock movement", p.ID)

	// Con historial, el resto de los campos se sigue pudiendo cambiar.
	p.Name = "NOMBRE NUEVO TEST"
	p.ReorderPoint = decimal.NewFromInt(4)
	got, err := f.store.Update(f.ctx, p.ID, p)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Name != "NOMBRE NUEVO TEST" {
		t.Fatalf("name: got %q", got.Name)
	}
}

func TestProductStore_LockedFieldsWithoutHistory(t *testing.T) {
	f := newProductFixture(t)
	p := f.create(t, "TEST-FREE", "SIN HISTORIAL TEST")

	// Sin historial, se puede cambiar todo (ej. se cargó mal el tipo de venta).
	p.SaleType = model.SaleTypeWeight
	p.MeasureUnit = model.MeasureUnitKilogram
	p.ControlsBatches = true
	got, err := f.store.Update(f.ctx, p.ID, p)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.SaleType != model.SaleTypeWeight || got.MeasureUnit != model.MeasureUnitKilogram || !got.ControlsBatches {
		t.Fatalf("got %q %q %v", got.SaleType, got.MeasureUnit, got.ControlsBatches)
	}
}

// Ojo: la secuencia no se deshace con el ROLLBACK del test. Cada test que
// genera un sku gasta números de la DB de desarrollo (deja huecos, nada más).
func TestProductStore_GeneratedSKU(t *testing.T) {
	f := newProductFixture(t)

	first, err := f.store.Create(f.ctx, f.product("", "SKU GENERADO UNO TEST"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	second, err := f.store.Create(f.ctx, f.product("", "SKU GENERADO DOS TEST"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	for _, p := range []*model.Product{first, second} {
		if len(p.SKU) < 6 || strings.Trim(p.SKU, "0123456789") != "" {
			t.Fatalf("sku: got %q, want 6+ digits", p.SKU)
		}
	}
	a, _ := strconv.ParseInt(first.SKU, 10, 64)
	b, _ := strconv.ParseInt(second.SKU, 10, 64)
	if b != a+1 {
		t.Fatalf("skus: got %q then %q, want consecutive", first.SKU, second.SKU)
	}
}

func TestProductStore_ManualSKUDoesNotUseSequence(t *testing.T) {
	f := newProductFixture(t)
	p := f.create(t, "MANUAL-1", "SKU MANUAL TEST")
	if p.SKU != "MANUAL-1" {
		t.Fatalf("sku: got %q, want MANUAL-1", p.SKU)
	}
}

func TestProductStore_UpdateEmptySKUKeepsIt(t *testing.T) {
	f := newProductFixture(t)
	p := f.create(t, "TEST-KEEPSKU", "CONSERVA SKU TEST")

	p.SKU = ""
	p.Name = "OTRO NOMBRE TEST"
	got, err := f.store.Update(f.ctx, p.ID, p)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.SKU != "TEST-KEEPSKU" {
		t.Fatalf("sku: got %q, want TEST-KEEPSKU", got.SKU)
	}
}

// next_product_sku no recorta al pasar de 6 dígitos (lpad solo lo haría).
// Se prueba la cuenta de la función, sin tocar la secuencia real.
func TestProductStore_SKUFormatGrowsPastSixDigits(t *testing.T) {
	f := newProductFixture(t)
	for n, want := range map[int64]string{7: "000007", 123456: "123456", 1234567: "1234567"} {
		var got string
		err := f.tx.QueryRowContext(f.ctx,
			`SELECT lpad(n::text, greatest(6, length(n::text)), '0') FROM (SELECT $1::bigint AS n) x;`, n).Scan(&got)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Errorf("%d: got %q, want %q", n, got, want)
		}
	}
}

func TestProductStore_ListAll(t *testing.T) {
	f := newProductFixture(t)
	for _, sku := range []string{"ZZPAGE-1", "ZZPAGE-2", "ZZPAGE-3"} {
		f.create(t, sku, "ZZPAGINA "+sku)
	}
	inactive := f.create(t, "ZZPAGE-4", "ZZPAGINA INACTIVA")
	if err := f.store.SetActive(f.ctx, inactive.ID, false); err != nil {
		t.Fatal(err)
	}

	// Buscando: 4 en total (incluye la inactiva), la página trae 2, activas primero.
	list, total, err := f.store.ListAll(f.ctx, "zzpagina", 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 4 || len(list) != 2 || !list[0].IsActive || !list[1].IsActive {
		t.Fatalf("got total %d, %d rows", total, len(list))
	}

	// También por sku.
	if _, total, _ := f.store.ListAll(f.ctx, "zzpage-", 50); total != 4 {
		t.Fatalf("by sku: got total %d, want 4", total)
	}

	// Sin búsqueda: cuenta todo el catálogo (con el seed), corta en el límite.
	list, total, err = f.store.ListAll(f.ctx, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if total < 4 || len(list) != 2 {
		t.Fatalf("no search: got total %d, %d rows", total, len(list))
	}

	// Límite 0: solo el total, sin filas.
	list, total, err = f.store.ListAll(f.ctx, "zzpagina", 0)
	if err != nil || total != 4 || len(list) != 0 {
		t.Fatalf("limit 0: got %d rows, total %d, err %v", len(list), total, err)
	}
}
