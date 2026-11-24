package web

import (
	"bytes"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
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

func TestSaleTypeOptionsHidesCompositeForNewProducts(t *testing.T) {
	if got := saleTypeOptions(""); slices.Contains(got, model.SaleTypeComposite) {
		t.Fatalf("new product: got %v, want no COMPOSITE", got)
	}
	if got := saleTypeOptions(model.SaleTypeComposite); !slices.Contains(got, model.SaleTypeComposite) {
		t.Fatalf("composite product: got %v, want COMPOSITE kept", got)
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
		})
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
		if p.BrandID != nil || p.PreferredSupplierID != nil || p.MaxStock != nil {
			t.Errorf("optionals: got %v %v %v, want nil", p.BrandID, p.PreferredSupplierID, p.MaxStock)
		}
		// Checkbox sin tildar: no viene en el formulario.
		if p.ControlsBatches || p.IsConsignment {
			t.Errorf("checkboxes: got %v %v, want false", p.ControlsBatches, p.IsConsignment)
		}
	})

	t.Run("unchecking a checkbox on edit", func(t *testing.T) {
		r := httptest.NewRequest("POST", "/products/1/edit", strings.NewReader("sku=A&name=AB&category_id=3"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		p := &model.Product{ControlsBatches: true, MaxStock: new(decimal.NewFromInt(9))}
		productFromForm(r, p)
		if p.ControlsBatches || p.MaxStock != nil {
			t.Errorf("got batches=%v max=%v, want cleared", p.ControlsBatches, p.MaxStock)
		}
	})

	badNumbers := []struct {
		field, wantKey string
	}{
		{"reorder_point", "error.product_reorder_point"},
		{"max_stock", "error.product_max_stock"},
		{"margin_percent", "error.product_margin"},
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
		MarginPercent: decimal.NewFromInt(25), IsActive: true,
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
			"SaleTypes":    saleTypeOptions(product.SaleType),
			"MeasureUnits": model.MeasureUnits,
		}
		if err := e.Render(&buf, "es", "product_form.gohtml", data); err != nil {
			t.Fatal(err)
		}
		html := buf.String()
		for _, s := range []string{
			`hx-post="/products/7/edit"`, `>MORTA-KG</p>`, `name="brand_q"`, `value="SANCOR"`, `value="2.5"`, `value="10"`,
			`<option value="WEIGHT" selected>`, `<option value="KILOGRAM" selected>`,
		} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})

	t.Run("new form", func(t *testing.T) {
		var buf bytes.Buffer
		data := map[string]any{"Product": &model.Product{}, "SaleTypes": saleTypeOptions(""), "MeasureUnits": model.MeasureUnits}
		if err := e.Render(&buf, "en", "product_form.gohtml", data); err != nil {
			t.Fatal(err)
		}
		html := buf.String()
		if !strings.Contains(html, `hx-post="/products/new"`) || strings.Contains(html, "COMPOSITE") {
			t.Error("new form: wrong action or offers COMPOSITE")
		}
		// El código interno lo genera la DB: el alta no tiene ese campo.
		if strings.Contains(html, `name="sku"`) {
			t.Error("new form: has an sku field")
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
