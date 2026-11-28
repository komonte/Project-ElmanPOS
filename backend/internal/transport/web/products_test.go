package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/createproduct"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/productcodes"
	"github.com/shopspring/decimal"
)

func TestSupplierOptionsKeepsInactiveCurrent(t *testing.T) {
	suppliers := []*model.Supplier{
		{ID: 1, Name: "Arcor", IsActive: true},
		{ID: 2, Name: "Proveedor viejo", IsActive: false},
	}

	if got := supplierOptions(suppliers, nil); len(got) != 1 {
		t.Fatalf("no supplier: got %+v", got)
	}
	if got := supplierOptions(suppliers, new(int64(2))); len(got) != 2 || !got[1].Selected {
		t.Fatalf("product with inactive supplier: got %+v", got)
	}
}

func TestProductCategoryOptions(t *testing.T) {
	all := testCategoryTree() // ver categories_test.go

	// Nuevo: solo las activas, con el camino completo y ordenadas por él.
	got := productCategoryOptions(all, 0)
	labels := make([]string, len(got))
	for i, o := range got {
		labels[i] = o.Label
	}
	want := []string{"Almacen", "Almacen › Galletitas", "Almacen › Galletitas › Dulces", "Almacen › Varios"}
	if !slices.Equal(labels, want) {
		t.Fatalf("new product: got %q, want %q", labels, want)
	}

	// En una categoría inactiva: la conserva, marcada.
	got = productCategoryOptions(all, 6)
	i := slices.IndexFunc(got, func(o productOption) bool { return o.ID == 6 })
	if i < 0 || !got[i].Selected || !got[i].Inactive || got[i].Label != "Limpieza › Varios" {
		t.Fatalf("product in inactive category: got %+v", got)
	}
}

func TestParseFormDecimal(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "0", true},
		{"  ", "0", true},
		{"10", "10", true},
		{"2.5", "2.5", true},
		{"2,5", "2.5", true}, // coma decimal, como se escribe acá
		{" 0,300 ", "0.3", true},
		{"abc", "0", false},
		{"1.234,5", "0", false}, // separador de miles: ambiguo, se rechaza
	}
	for _, tt := range tests {
		got, ok := parseFormDecimal(tt.in)
		if ok != tt.ok || !got.Equal(decimal.RequireFromString(tt.want)) {
			t.Errorf("%q: got %s %v, want %s %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestProductFromForm(t *testing.T) {
	post := func(v url.Values) *model.Product {
		r := httptest.NewRequest("POST", "/products/new", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		p := &model.Product{}
		if errKey := productFromForm(r, p); errKey != "" {
			t.Fatalf("unexpected error key %q", errKey)
		}
		return p
	}

	t.Run("all fields", func(t *testing.T) {
		p := post(url.Values{
			"sku": {"oreo-200"}, "name": {"galletitas oreo"}, "description": {"x"},
			"category_id": {"3"}, "brand_id": {"2"}, "preferred_supplier_id": {"5"},
			"sale_type": {"WEIGHT"}, "measure_unit": {"KILOGRAM"},
			"controls_batches": {"on"}, "is_consignment": {"on"},
			"reorder_point": {"2,5"}, "max_stock": {"10"}, "margin_percent": {"30"},
			"shelf_life_days": {" 21 "},
		})
		if p.ShelfLifeDays == nil || *p.ShelfLifeDays != 21 {
			t.Errorf("shelf life: got %v, want 21", p.ShelfLifeDays)
		}
		if p.CategoryID != 3 || p.BrandID == nil || *p.BrandID != 2 || p.PreferredSupplierID == nil || *p.PreferredSupplierID != 5 {
			t.Errorf("ids: got %d %v %v", p.CategoryID, p.BrandID, p.PreferredSupplierID)
		}
		if !p.ControlsBatches || !p.IsConsignment {
			t.Errorf("checkboxes: got %v %v", p.ControlsBatches, p.IsConsignment)
		}
		if !p.ReorderPoint.Equal(decimal.RequireFromString("2.5")) || p.MaxStock == nil || !p.MaxStock.Equal(decimal.NewFromInt(10)) {
			t.Errorf("numbers: got %s %v", p.ReorderPoint, p.MaxStock)
		}
	})

	t.Run("empty optionals", func(t *testing.T) {
		p := post(url.Values{"sku": {"A"}, "name": {"AB"}, "category_id": {"3"}})
		if p.BrandID != nil || p.PreferredSupplierID != nil || p.MaxStock != nil || p.ShelfLifeDays != nil {
			t.Errorf("optionals: got %v %v %v %v, want nil", p.BrandID, p.PreferredSupplierID, p.MaxStock, p.ShelfLifeDays)
		}
		// Checkbox sin tildar: no viene en el formulario.
		if p.ControlsBatches || p.IsConsignment {
			t.Errorf("checkboxes: got %v %v, want false", p.ControlsBatches, p.IsConsignment)
		}
	})

	t.Run("unchecking a checkbox on edit", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/products/1/edit", strings.NewReader("sku=A&name=AB&category_id=3"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		p := &model.Product{ControlsBatches: true, MaxStock: new(decimal.NewFromInt(9)), ShelfLifeDays: new(21)}
		productFromForm(r, p)
		if p.ControlsBatches || p.MaxStock != nil || p.ShelfLifeDays != nil {
			t.Errorf("got batches=%v max=%v shelf life=%v, want cleared", p.ControlsBatches, p.MaxStock, p.ShelfLifeDays)
		}
	})

	badNumbers := []struct {
		field, wantKey string
	}{
		{"reorder_point", "error.product_reorder_point"},
		{"max_stock", "error.product_max_stock"},
		{"margin_percent", "error.product_margin"},
		{"shelf_life_days", "error.product_shelf_life"},
	}
	// Días de vida con decimales: no es un número de días.
	r := httptest.NewRequest("POST", "/products/new", strings.NewReader("sku=A&name=AB&category_id=3&shelf_life_days=2,5"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if got := productFromForm(r, &model.Product{}); got != "error.product_shelf_life" {
		t.Errorf("shelf life 2,5: got %q", got)
	}
	for _, tt := range badNumbers {
		t.Run("bad "+tt.field, func(t *testing.T) {
			v := url.Values{"sku": {"A"}, "name": {"AB"}, "category_id": {"3"}, tt.field: {"diez"}}
			r := httptest.NewRequest("POST", "/products/new", strings.NewReader(v.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			p := &model.Product{}
			if got := productFromForm(r, p); got != tt.wantKey {
				t.Fatalf("got %q, want %q", got, tt.wantKey)
			}
			if p.SKU != "A" {
				t.Errorf("other fields not copied: sku %q", p.SKU)
			}
		})
	}
}

// Los templates se dibujan con datos reales: un error de template (un campo
// mal escrito, un printf roto) recién aparece al ejecutarlo.
func TestRenderProductPartials(t *testing.T) {
	e := NewTemplateEngine()
	product := &model.Product{
		ID: 7, SKU: "MORTA-KG", Name: "MORTADELA", BrandName: "SANCOR", CategoryName: "Fiambreria",
		SaleType: model.SaleTypeWeight, MeasureUnit: model.MeasureUnitKilogram,
		ReorderPoint: decimal.RequireFromString("2.5"), MaxStock: new(decimal.NewFromInt(10)),
		MarginPercent: decimal.NewFromInt(25), ShelfLifeDays: new(21), IsActive: true,
	}

	t.Run("table truncated", func(t *testing.T) {
		var buf bytes.Buffer
		data := map[string]any{"Products": []*model.Product{product}, "Shown": 50, "Total": 1234}
		if err := e.Render(&buf, "es", "product_table.gohtml", data); err != nil {
			t.Fatal(err)
		}
		for _, s := range []string{"MORTADELA", "SANCOR", "A granel (por kg)", "primeros 50 de 1234"} {
			if !strings.Contains(buf.String(), s) {
				t.Errorf("missing %q", s)
			}
		}
	})

	t.Run("table shows the current price", func(t *testing.T) {
		priced := *product
		priced.ID = 8
		priced.Name = "QUESO CON PRECIO"
		priced.CurrentPrice = new(decimal.RequireFromString("1500.5"))
		var buf bytes.Buffer
		data := map[string]any{"Products": []*model.Product{&priced, product}, "Shown": 2, "Total": 2}
		if err := e.Render(&buf, "es", "product_table.gohtml", data); err != nil {
			t.Fatal(err)
		}
		html := buf.String()
		// El que tiene precio lo muestra; el que no, una raya.
		for _, s := range []string{">Precio<", "$ 1.500,50", "—"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})

	t.Run("table complete has no notice", func(t *testing.T) {
		var buf bytes.Buffer
		data := map[string]any{"Products": []*model.Product{product}, "Shown": 1, "Total": 1}
		if err := e.Render(&buf, "es", "product_table.gohtml", data); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), "primeros") {
			t.Error("notice shown for a complete list")
		}
	})

	t.Run("edit form", func(t *testing.T) {
		var buf bytes.Buffer
		data := map[string]any{
			"Product":      product,
			"Categories":   []productOption{{ID: 3, Label: "Fiambreria", Selected: true}},
			"BrandName":    "SANCOR",
			"Suppliers":    []productOption{},
			"SaleTypes":    model.SaleTypes,
			"MeasureUnits": model.MeasureUnits,
		}
		if err := e.Render(&buf, "es", "product_form.gohtml", data); err != nil {
			t.Fatal(err)
		}
		html := buf.String()
		for _, s := range []string{
			`hx-post="/products/7/edit"`, `>MORTA-KG</p>`, `name="brand_q"`, `value="SANCOR"`, `value="2.5"`, `value="10"`,
			`<option value="WEIGHT" selected>`, `<option value="KILOGRAM" selected>`,
			`name="shelf_life_days"`, "Días de vida",
		} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		// Al editar, los códigos de barra tienen su propia sección, fuera del
		// <form> del producto (no puede haber un form dentro de otro).
		if strings.Contains(html, `name="barcode"`) {
			t.Error("edit form: has a barcode field")
		}
		if !strings.Contains(html, `name="name" autofocus`) {
			t.Error("edit form: name should have the focus")
		}
		if !regexp.MustCompile(`name="shelf_life_days"[^>]*value="21"`).MatchString(html) {
			t.Error("edit form: shelf life should show 21")
		}
		section := strings.Index(html, `id="product-codes"`)
		if section < 0 || section < strings.Index(html, "</form>") {
			t.Error("edit form: barcode section missing or inside the product form")
		}
	})

	t.Run("new form", func(t *testing.T) {
		var buf bytes.Buffer
		data := map[string]any{"Product": &model.Product{}, "SaleTypes": model.SaleTypes, "MeasureUnits": model.MeasureUnits,
			"Barcode": "7790070991232"}
		if err := e.Render(&buf, "en", "product_form.gohtml", data); err != nil {
			t.Fatal(err)
		}
		html := buf.String()
		// Un combo se crea vacío y sus componentes se cargan al editarlo.
		if !strings.Contains(html, `hx-post="/products/new"`) || !strings.Contains(html, `value="COMPOSITE"`) {
			t.Error("new form: wrong action or does not offer COMPOSITE")
		}
		if strings.Contains(html, `id="product-components"`) {
			t.Error("new form: the components section is only on edit")
		}
		// El código interno lo genera la DB: el alta no tiene ese campo.
		if strings.Contains(html, `name="sku"`) {
			t.Error("new form: has an sku field")
		}
		// El código de barras sí, y vuelve con lo escrito si hubo un error.
		if !strings.Contains(html, `name="barcode"`) || !strings.Contains(html, `value="7790070991232"`) {
			t.Error("new form: missing barcode field or its value")
		}
		// Se escanea primero: el código va antes que el nombre y tiene el foco.
		barcode, name := strings.Index(html, `id="product-barcode"`), strings.Index(html, `id="product-name"`)
		if barcode < 0 || barcode > name {
			t.Error("new form: barcode is not the first field")
		}
		if strings.Count(html, "autofocus") != 1 || !strings.Contains(html[barcode:name], "autofocus") {
			t.Error("new form: autofocus should be on the barcode only")
		}
		// Sin stock máximo el campo queda vacío, no "<nil>".
		if !strings.Contains(html, `name="max_stock" inputmode="decimal" aria-describedby="product-max-stock-hint"
                     value=""`) {
			t.Error("max_stock should be empty")
		}
	})
}

func TestProductFromFormBrandTypedButNotPicked(t *testing.T) {
	post := func(body string) (string, *model.Product) {
		r := httptest.NewRequest("POST", "/products/new", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		p := &model.Product{}
		return productFromForm(r, p), p
	}

	// Escribió "arc" pero no eligió de la lista: se avisa.
	if key, _ := post("name=AB&category_id=3&brand_q=arc&brand_id="); key != "error.product_brand_pick" {
		t.Fatalf("typed, not picked: got %q", key)
	}
	// Eligió de la lista: vale.
	if key, p := post("name=AB&category_id=3&brand_q=ARCOR&brand_id=4"); key != "" || p.BrandID == nil || *p.BrandID != 4 {
		t.Fatalf("picked: got %q %v", key, p.BrandID)
	}
	// Sin texto ni marca: sin marca.
	if key, p := post("name=AB&category_id=3&brand_q=&brand_id="); key != "" || p.BrandID != nil {
		t.Fatalf("empty: got %q %v", key, p.BrandID)
	}
	// Eligió "Crear marca": no hay id, pero hay marca nueva. Vale.
	if key, p := post("name=AB&category_id=3&brand_q=NUEVA&brand_id=&brand_new=NUEVA"); key != "" || p.BrandID != nil {
		t.Fatalf("new brand: got %q %v", key, p.BrandID)
	}
}

func TestNewBrandOption(t *testing.T) {
	found := []*model.Brand{{ID: 1, Name: "COCA-COLA"}, {ID: 2, Name: "ARCOR"}}
	cases := []struct{ query, want string }{
		{"  coca   cola ", "COCA COLA"}, // parecida a COCA-COLA, pero no igual: se ofrece
		{"arcor", ""},                   // ya está en la lista: no se ofrece
		{"a", ""},                       // nombre muy corto: no es una marca válida
		{"", ""},
	}
	for _, tc := range cases {
		if got := newBrandOption(tc.query, found); got != tc.want {
			t.Errorf("newBrandOption(%q) = %q, want %q", tc.query, got, tc.want)
		}
	}
}

func TestRenderBrandOptionsCreate(t *testing.T) {
	e := NewTemplateEngine()
	var buf bytes.Buffer
	data := map[string]any{"Brands": []*model.Brand{{ID: 1, Name: "COCA-COLA"}}, "Query": "coca cola", "NewBrand": "COCA COLA"}
	if err := e.Render(&buf, "es", "product_brand_options.gohtml", data); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	existing, create := strings.Index(html, "COCA-COLA"), strings.Index(html, "Crear marca «COCA COLA»")
	if existing < 0 || create < 0 || create < existing {
		t.Errorf("want the similar brand first and then the create option, got %s", html)
	}
	if strings.Contains(html, "No hay marcas") {
		t.Error("not-found notice shown with results")
	}
}

func TestRenderProductFormBrandNew(t *testing.T) {
	e := NewTemplateEngine()
	base := func(p *model.Product, brandNew string) string {
		var buf bytes.Buffer
		data := map[string]any{"Product": p, "SaleTypes": model.SaleTypes, "MeasureUnits": model.MeasureUnits, "BrandNew": brandNew}
		if err := e.Render(&buf, "es", "product_form.gohtml", data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	// Alta que vuelve con un error: la marca nueva sigue elegida y avisada.
	html := base(&model.Product{}, "MARCA NUEVA")
	for _, s := range []string{`name="brand_new" value="MARCA NUEVA"`, `name="brand_q"`, `value="MARCA NUEVA"`,
		`hx-get="/products/brand-options?new=1"`, `id="product-brand-new" class="mt-1 text-sm font-bold text-tag" >`} {
		if !strings.Contains(html, s) {
			t.Errorf("new form: missing %q", s)
		}
	}

	// Edición: no se ofrece crear marcas, y el aviso está oculto.
	html = base(&model.Product{ID: 7, Name: "X"}, "")
	if strings.Contains(html, "?new=1") || !strings.Contains(html, `text-tag" hidden>`) {
		t.Error("edit form: offers to create brands or shows the new-brand note")
	}
}

func TestRenderBrandOptions(t *testing.T) {
	e := NewTemplateEngine()
	var buf bytes.Buffer
	data := map[string]any{"Brands": []*model.Brand{{ID: 4, Name: "ARCOR"}}, "Query": "arc"}
	if err := e.Render(&buf, "es", "product_brand_options.gohtml", data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `data-id="4"`) || !strings.Contains(buf.String(), "ARCOR") {
		t.Fatalf("got %s", buf.String())
	}

	buf.Reset()
	if err := e.Render(&buf, "es", "product_brand_options.gohtml", map[string]any{"Query": "zzz"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "No hay marcas") {
		t.Fatalf("no results: got %s", buf.String())
	}
}

func TestErrMsgBarcode(t *testing.T) {
	h := &WebHandler{}
	r := httptest.NewRequest(http.MethodPost, "/products/new", nil) // sin idioma: "en"

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"taken names the owner",
			&productcodes.CodeTakenError{Code: "7790070991232", ProductSKU: "000123", ProductName: "SODA COLA 1.5L"},
			"That barcode already belongs to product 000123 SODA COLA 1.5L."},
		{"taken without owner (race)",
			fmt.Errorf("%w: %w", service.ErrAlreadyExists, store.ErrDuplicateCode),
			"Another product already has that barcode."},
		{"new brand that exists inactive",
			&createproduct.BrandInactiveError{Name: "ARCOR"},
			"Brand ARCOR already exists, but it is inactive. Activate it in Brands or choose another one."},
		{"new category error says so",
			fmt.Errorf("%w: %w: %w", createproduct.ErrNewCategory, service.ErrValidation, model.ErrCategoryTaxRequired),
			"New category: Choose the category's VAT rate."},
		{"invalid characters",
			fmt.Errorf("%w: %w", service.ErrValidation, model.ErrBarcodeNotDigits),
			"A barcode can only contain digits."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.errMsg(r, tc.err); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderProductCodes(t *testing.T) {
	e := NewTemplateEngine()
	codes := []*model.ProductCode{
		{ID: 4, ProductID: 7, CodeType: model.CodeTypeEAN13, Code: "7790070991232"},
		{ID: 5, ProductID: 7, CodeType: model.CodeTypeInternal, Code: "0000009000001"},
	}

	t.Run("list", func(t *testing.T) {
		var buf bytes.Buffer
		data := map[string]any{"ProductID": int64(7), "Codes": codes}
		if err := e.Render(&buf, "es", "product_codes.gohtml", data); err != nil {
			t.Fatal(err)
		}
		html := buf.String()
		for _, s := range []string{
			"7790070991232", "EAN-13", "0000009000001", "Interno",
			`hx-delete="/products/7/codes/4"`, `hx-post="/products/7/codes"`,
		} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if strings.Contains(html, "autofocus") || strings.Contains(html, `role="alert"`) {
			t.Error("plain list: unexpected autofocus or alert")
		}
	})

	t.Run("error keeps the typed code and the focus", func(t *testing.T) {
		var buf bytes.Buffer
		data := map[string]any{"ProductID": int64(7), "Codes": []*model.ProductCode{},
			"CodeError": "Ese código de barras ya es del producto 000123 X.", "CodeValue": "7790070991232", "Focus": true}
		if err := e.Render(&buf, "es", "product_codes.gohtml", data); err != nil {
			t.Fatal(err)
		}
		html := buf.String()
		for _, s := range []string{`role="alert"`, "ya es del producto 000123 X.", `value="7790070991232"`, "autofocus",
			"no tiene códigos de barra"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})
}

// checkCodes es un store de códigos de mentira para productcodes.Service:
// solo FindByCode importa (Check no escribe). 7790070991232 es de OREO (id 1).
type checkCodes struct{}

func (checkCodes) FindByCode(_ context.Context, code string) (*model.ProductCode, error) {
	if code == "7790070991232" {
		return &model.ProductCode{ID: 9, ProductID: 1, Code: code, ProductSKU: "ORE-200G", ProductName: "GALLETITAS OREO 200G"}, nil
	}
	return nil, store.ErrNotFound
}
func (checkCodes) ListByProduct(context.Context, int64) ([]*model.ProductCode, error) {
	return nil, nil
}
func (checkCodes) Add(context.Context, *model.ProductCode) (*model.ProductCode, error) {
	return nil, errors.New("not used")
}
func (checkCodes) Delete(context.Context, int64, int64) error { return errors.New("not used") }

func TestProductsBarcodeCheck(t *testing.T) {
	h := &WebHandler{engine: NewTemplateEngine(), productCodes: productcodes.New(nil, checkCodes{})}

	cases := []struct {
		name    string
		barcode string
		want    []string // vacío: la respuesta no muestra nada
	}{
		{"taken offers to edit the owner", "7790070991232",
			[]string{"already belongs to product ORE-200G GALLETITAS OREO 200G", `hx-get="/products/1/edit"`}},
		{"free shows nothing", "40170725", nil},
		{"empty shows nothing", "  ", nil},
		{"invalid", "77-90", []string{"A barcode can only contain digits."}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/products/barcode-check?barcode="+url.QueryEscape(tc.barcode), nil)
			rec := httptest.NewRecorder()
			h.ProductsBarcodeCheck(rec, r)

			body := rec.Body.String()
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, body)
			}
			if tc.want == nil && strings.Contains(body, `role="alert"`) {
				t.Errorf("got an alert: %s", body)
			}
			for _, s := range tc.want {
				if !strings.Contains(body, s) {
					t.Errorf("missing %q in %s", s, body)
				}
			}
		})
	}
}

func TestNewCategoryParentOptions(t *testing.T) {
	root := int64(1)
	all := []*model.Category{
		{ID: 1, Name: "Almacen", TaxID: 10, IsActive: true},
		{ID: 2, Name: "Galletitas", ParentID: &root, TaxID: 11, IsActive: true},
		{ID: 3, Name: "Vieja", TaxID: 10, IsActive: false},
	}
	selected := int64(2)
	got := newCategoryParentOptions(all, &selected)

	// Solo activas (un padre inactivo lo rechaza el store), con camino e IVA.
	if len(got) != 2 {
		t.Fatalf("got %d options, want 2: %+v", len(got), got)
	}
	if got[1].Label != "Almacen › Galletitas" || got[1].TaxID != 11 || !got[1].Selected || got[0].Selected {
		t.Errorf("got %+v", got)
	}
}

func TestNewCategoryFromForm(t *testing.T) {
	form := func(body string) *model.Category {
		r := httptest.NewRequest("POST", "/products/new", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return newCategoryFromForm(r)
	}

	// Sin abrir el subformulario: nada que crear.
	if c := form("category_id=3&new_category_name=X"); c != nil {
		t.Errorf("closed: got %+v", c)
	}
	c := form("category_new=1&new_category_name=Galletitas&new_category_parent_id=1&new_category_tax_id=10")
	if c == nil || c.Name != "Galletitas" || c.ParentID == nil || *c.ParentID != 1 || c.TaxID != 10 {
		t.Errorf("open: got %+v", c)
	}
	// Sin padre: categoría principal.
	if c := form("category_new=1&new_category_name=Bebidas&new_category_parent_id=&new_category_tax_id=10"); c == nil || c.ParentID != nil {
		t.Errorf("no parent: got %+v", c)
	}
}

func TestRenderProductFormNewCategory(t *testing.T) {
	e := NewTemplateEngine()
	render := func(data map[string]any) string {
		data["SaleTypes"] = model.SaleTypes
		data["MeasureUnits"] = model.MeasureUnits
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", "product_form.gohtml", data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	parents := []productOption{{ID: 1, Label: "Almacen", TaxID: 10}}
	taxes := []productOption{{ID: 10, Label: "IVA 21%"}}

	// Cerrado: el link para abrirlo, y los campos deshabilitados (no se mandan).
	html := render(map[string]any{"Product": &model.Product{}, "CategoryParents": parents, "Taxes": taxes})
	for _, s := range []string{`+ Nueva categoría`, `name="category_new" value="1" disabled`,
		`name="new_category_name" required maxlength="100"
                       value="" disabled`, `data-tax="10"`} {
		if !strings.Contains(html, s) {
			t.Errorf("closed: missing %q", s)
		}
	}
	if strings.Contains(html, `name="category_id" required aria-describedby="product-category-hint" class="field" disabled`) {
		t.Error("closed: category select is disabled")
	}

	// Vuelve con un error: abierto, con lo escrito, y el select deshabilitado.
	parent := int64(1)
	taxes[0].Selected = true
	html = render(map[string]any{"Product": &model.Product{}, "CategoryParents": parents, "Taxes": taxes,
		"NewCategory": &model.Category{Name: "Galletitas", ParentID: &parent, TaxID: 10}})
	for _, s := range []string{`name="category_new" value="1" >`, `value="Galletitas"`, `<option value="10" data-vat="" selected>IVA 21%`,
		`class="field" disabled>`} {
		if !strings.Contains(html, s) {
			t.Errorf("open: missing %q", s)
		}
	}

	// Edición: no se crean categorías.
	html = render(map[string]any{"Product": &model.Product{ID: 7, Name: "X"}})
	if strings.Contains(html, "category_new") {
		t.Error("edit form: offers a new category")
	}
}

func TestRenderProductPrices(t *testing.T) {
	e := NewTemplateEngine()
	history := []*model.Price{
		{ID: 3, ProductID: 7, UnitPrice: decimal.RequireFromString("1600"), ValidFrom: "2026-11-25T15:00:00Z"},
		{ID: 2, ProductID: 7, UnitPrice: decimal.RequireFromString("1500.5"), ValidFrom: "2026-11-01T09:30:00Z"},
	}

	render := func(t *testing.T, data map[string]any) string {
		t.Helper()
		var buf bytes.Buffer
		if _, ok := data["Can"]; !ok {
			data["Can"] = adminCan
		}
		if err := e.Render(&buf, "es", "product_prices.gohtml", data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	t.Run("current and history", func(t *testing.T) {
		html := render(t, map[string]any{"ProductID": int64(7), "Prices": history})
		for _, s := range []string{
			// Solo el día: la hora depende de la zona en que se muestran las
			// fechas, y eso no es lo que prueba este test.
			"$ 1.600,00", "desde 2026-11-25 ", // vigente
			"Precios anteriores", "$ 1.500,50", // historia
			`hx-post="/products/7/prices"`,
		} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if strings.Contains(html, "autofocus") || strings.Contains(html, `role="alert"`) {
			t.Error("plain section: unexpected autofocus or alert")
		}
	})

	t.Run("without permission: the price, no form", func(t *testing.T) {
		html := render(t, map[string]any{"ProductID": int64(7), "Prices": history, "Can": stockCan})
		if !strings.Contains(html, "$ 1.600,00") || strings.Contains(html, "hx-post") {
			t.Errorf("got %s", html)
		}
	})

	t.Run("one price has no history", func(t *testing.T) {
		html := render(t, map[string]any{"ProductID": int64(7), "Prices": history[:1]})
		if !strings.Contains(html, "$ 1.600,00") || strings.Contains(html, "Precios anteriores") {
			t.Errorf("got %s", html)
		}
	})

	t.Run("no price", func(t *testing.T) {
		html := render(t, map[string]any{"ProductID": int64(7), "Prices": []*model.Price{}})
		if !strings.Contains(html, "todavía no tiene precio") {
			t.Errorf("got %s", html)
		}
	})

	t.Run("error keeps the typed price and the focus", func(t *testing.T) {
		html := render(t, map[string]any{"ProductID": int64(7), "Prices": []*model.Price{},
			"PriceError": "El precio tiene que ser un número mayor que 0, con hasta 2 decimales.", "PriceValue": "12,345"})
		for _, s := range []string{`role="alert"`, "mayor que 0", `value="12,345"`, "autofocus"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})
}

func TestRenderProductCosts(t *testing.T) {
	e := NewTemplateEngine()
	dec := decimal.RequireFromString
	history := []*model.Cost{
		{ID: 5, ProductID: 7, UnitCost: dec("700"), AvgCost: dec("650.5"), ValidFrom: "2026-11-25T15:00:00Z"},
		{ID: 4, ProductID: 7, UnitCost: dec("600"), AvgCost: dec("600"), ValidFrom: "2026-11-01T09:30:00Z"},
	}
	suggested := dec("1076.92")

	render := func(t *testing.T, data map[string]any) string {
		t.Helper()
		var buf bytes.Buffer
		if _, ok := data["Can"]; !ok {
			data["Can"] = adminCan
		}
		if err := e.Render(&buf, "es", "product_costs.gohtml", data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	t.Run("without permission: costs and suggestion, no forms", func(t *testing.T) {
		html := render(t, map[string]any{"ProductID": int64(7), "Costs": history,
			"Margin": dec("35"), "Suggested": &suggested, "Can": stockCan})
		if !strings.Contains(html, "$ 700,00") || !strings.Contains(html, "$ 1.076,92") ||
			strings.Contains(html, "Usar como precio") || strings.Contains(html, "hx-post") {
			t.Errorf("got %s", html)
		}
	})

	t.Run("costs, suggestion and history", func(t *testing.T) {
		html := render(t, map[string]any{"ProductID": int64(7), "Costs": history,
			"Margin": dec("35"), "Suggested": &suggested})
		for _, s := range []string{
			"Costo último", "$ 700,00", "Costo promedio", "$ 650,50", "desde 2026-11-25 ", // vigente
			"Precio sugerido con margen 35%", "$ 1.076,92", // sugerencia
			// "Usar como precio" manda el número sin formato a la sección de precio.
			`hx-post="/products/7/prices"`, `hx-target="#product-prices"`, `name="unit_price" value="1076.92"`,
			`hx-post="/products/7/costs"`,
			"Costos anteriores", "$ 600,00", // historia
		} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if strings.Contains(html, "autofocus") || strings.Contains(html, `role="alert"`) {
			t.Error("plain section: unexpected autofocus or alert")
		}
	})

	t.Run("decimal margin uses a comma", func(t *testing.T) {
		html := render(t, map[string]any{"ProductID": int64(7), "Costs": history[:1],
			"Margin": dec("37.5"), "Suggested": &suggested})
		if !strings.Contains(html, "margen 37,5%") || strings.Contains(html, "Costos anteriores") {
			t.Errorf("got %s", html)
		}
	})

	t.Run("margin 0: no suggestion", func(t *testing.T) {
		html := render(t, map[string]any{"ProductID": int64(7), "Costs": history[:1], "Margin": dec("0")})
		if strings.Contains(html, "Usar como precio") || !strings.Contains(html, "margen 0") {
			t.Errorf("got %s", html)
		}
	})

	t.Run("no cost", func(t *testing.T) {
		html := render(t, map[string]any{"ProductID": int64(7), "Costs": []*model.Cost{}, "Margin": dec("30")})
		if !strings.Contains(html, "todavía no tiene costo") || strings.Contains(html, "Usar como precio") ||
			strings.Contains(html, "margen 0") {
			t.Errorf("got %s", html)
		}
	})

	t.Run("error keeps the typed cost and the focus", func(t *testing.T) {
		html := render(t, map[string]any{"ProductID": int64(7), "Costs": []*model.Cost{}, "Margin": dec("30"),
			"CostError": "El costo tiene que ser un número mayor que 0.", "CostValue": "12,345"})
		for _, s := range []string{`role="alert"`, "mayor que 0", `value="12,345"`, "autofocus"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})
}

// Al editar, el formulario incluye la sección de costo; al dar de alta, no.
func TestRenderProductFormCostSection(t *testing.T) {
	e := NewTemplateEngine()
	render := func(data map[string]any) string {
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", "product_form.gohtml", data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	html := render(map[string]any{"Product": &model.Product{ID: 7, Name: "X"},
		"CostData": map[string]any{"ProductID": int64(7), "Costs": []*model.Cost{}, "Margin": decimal.Zero}})
	if !strings.Contains(html, `id="product-costs"`) {
		t.Error("edit form: missing the cost section")
	}
	if html := render(map[string]any{"Product": &model.Product{}}); strings.Contains(html, `id="product-costs"`) {
		t.Error("new product form: has a cost section")
	}
}
