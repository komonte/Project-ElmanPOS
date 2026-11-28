package web

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

func TestQuickProductPrefill(t *testing.T) {
	tests := []struct {
		q                  string
		wantName, wantCode string
	}{
		{"7790895001611", "", "7790895001611"}, // escaneado
		{" 9000001 ", "", "9000001"},           // código corto, con espacios
		{"queso cremoso", "queso cremoso", ""}, // nombre
		{"7up 1.5L", "7up 1.5L", ""},           // empieza con número pero no es código
		{"000", "000", ""},                     // solo ceros: no es un código válido
	}
	for _, tt := range tests {
		name, code := quickProductPrefill(tt.q)
		if name != tt.wantName || code != tt.wantCode {
			t.Errorf("prefill(%q) = name %q, code %q; want %q, %q", tt.q, name, code, tt.wantName, tt.wantCode)
		}
	}
}

func TestProductChosenTrigger(t *testing.T) {
	got, err := productChosenTrigger(&model.Product{ID: 42, Name: `QUESO "CREMOSO"`, ControlsBatches: true,
		ShelfLifeDays: new(21), SaleType: model.SaleTypeWeight})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"productChosen":{"batches":true,"id":42,"name":"QUESO \"CREMOSO\"","shelf_life":21,"weight":true}}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	// Sin días de vida: null (no se precarga el vencimiento).
	got, _ = productChosenTrigger(&model.Product{ID: 1, Name: "SODA"})
	if !strings.Contains(got, `"shelf_life":null`) {
		t.Errorf("no shelf life: got %s", got)
	}
}

// El alta desde un renglón (compra o conteo) es el formulario de Productos
// en modo renglón.
func TestRenderProductFormForLine(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, name string, data map[string]any) string {
		t.Helper()
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", name, data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	supplierID := int64(1)
	base := func() map[string]any {
		return map[string]any{
			"Product":    &model.Product{PreferredSupplierID: &supplierID},
			"Categories": []productOption{{ID: 4, Label: "Almacén > Lácteos"}},
			"Suppliers":  []productOption{{ID: 1, Label: "Distribuidora del Sur S.A.", Selected: true}},
			"SaleTypes":  model.SaleTypes, "MeasureUnits": model.MeasureUnits,
		}
	}

	t.Run("scanned code: focus on the name", func(t *testing.T) {
		data := base()
		data["ForLine"], data["Barcode"], data["Focus"] = true, "7790895001611", "name"
		html := render(t, "product_form.gohtml", data)
		for _, s := range []string{`hx-post="/picker/quick-product"`, `value="7790895001611"`,
			`/products/barcode-check?for=line`, "pickerSwitchToAssign()", "+ Nueva categoría",
			`value="1" selected`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if strings.Contains(html, `hx-post="/products/new"`) {
			t.Error("must not save to /products/new (it redirects out of the purchase or count)")
		}
		if !regexpMatch(`id="product-name" name="name" autofocus`, html) {
			t.Error("the name should have the focus")
		}
		if regexpMatch(`id="product-barcode"[^>]*autofocus`, html) {
			t.Error("the barcode should not have the focus")
		}
	})

	t.Run("typed name: focus on the category", func(t *testing.T) {
		data := base()
		data["Product"].(*model.Product).Name = "QUESO CREMOSO"
		data["ForLine"], data["Focus"] = true, "category"
		html := render(t, "product_form.gohtml", data)
		if !regexpMatch(`id="product-category"[^>]*autofocus`, html) {
			t.Error("the category should have the focus")
		}
		// Sin código no hay a qué producto agregárselo.
		if strings.Contains(html, "pickerSwitchToAssign") {
			t.Error("without a code it should not offer to assign it")
		}
	})

	t.Run("products page is unchanged", func(t *testing.T) {
		html := render(t, "product_form.gohtml", base())
		if !strings.Contains(html, `hx-post="/products/new"`) || strings.Contains(html, "?for=line") ||
			!regexpMatch(`id="product-barcode"[^>]*autofocus`, html) {
			t.Errorf("products page form changed: %s", html)
		}
	})

	t.Run("barcode taken: use it from a line, edit it from products", func(t *testing.T) {
		owner := &model.Product{ID: 3, Name: "MORTADELA", ControlsBatches: true, ShelfLifeDays: new(21)}
		use := render(t, "product_barcode_check.gohtml", map[string]any{"ForLine": true,
			"Message": "Ya es de MORTADELA", "OwnerID": int64(3), "Owner": owner})
		for _, s := range []string{`data-id="3"`, `data-name="MORTADELA"`, `data-batches="true"`, `data-shelf-life="21"`,
			"pickerUseExisting(this)", "Usar ese producto en el renglón"} {
			if !strings.Contains(use, s) {
				t.Errorf("purchase: missing %q", s)
			}
		}
		if strings.Contains(use, "/products/3/edit") {
			t.Error("purchase: must not offer to edit (it leaves the purchase)")
		}
		// El dueño no se puede recibir (inactivo o combo): solo el aviso.
		none := render(t, "product_barcode_check.gohtml", map[string]any{"ForLine": true,
			"Message": "Ya es de COMBO", "OwnerID": int64(9)})
		if strings.Contains(none, "<button") {
			t.Errorf("purchase, owner not receivable: got %s", none)
		}
		edit := render(t, "product_barcode_check.gohtml", map[string]any{"Message": "Ya es de MORTADELA", "OwnerID": int64(3)})
		if !strings.Contains(edit, `hx-get="/products/3/edit"`) || strings.Contains(edit, "pickerUseExisting") {
			t.Errorf("products: got %s", edit)
		}
	})
}

func TestRenderAssignCode(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, name string, data map[string]any) string {
		t.Helper()
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", name, data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	panel := render(t, "picker_assign_code.gohtml", map[string]any{"Code": "7790000000017",
		"Error": "Ese código de barras ya es del producto 000002 SODA COLA 1.5L."})
	for _, s := range []string{"Agregar código a un producto", `role="alert"`, "000002 SODA COLA 1.5L",
		`hx-get="/picker/assign-code/options"`, `hx-include="#assign-code-value"`,
		`id="assign-code-value" name="code" value="7790000000017"`} {
		if !strings.Contains(panel, s) {
			t.Errorf("panel: missing %q", s)
		}
	}
	// El buscador no va en un <form>: Enter (el escáner) no envía nada.
	if strings.Contains(panel, "<form") {
		t.Error("panel: the search must not be inside a form")
	}

	opts := render(t, "picker_assign_code_options.gohtml", map[string]any{"Query": "soda", "Code": "7790000000017",
		"Products": []*model.Product{{ID: 2, SKU: "000002", Name: "SODA COLA 1.5L"}}})
	for _, s := range []string{`hx-post="/picker/assign-code"`, `name="code" value="7790000000017"`,
		`name="product_id" value="2"`, "SODA COLA 1.5L"} {
		if !strings.Contains(opts, s) {
			t.Errorf("options: missing %q", s)
		}
	}
	none := render(t, "picker_assign_code_options.gohtml", map[string]any{"Query": "zzz", "Products": []*model.Product{}})
	if !strings.Contains(none, "No hay productos activos") {
		t.Errorf("options: no results: got %s", none)
	}
}

func regexpMatch(pattern, s string) bool { return regexp.MustCompile(pattern).MatchString(s) }

// La unidad junto a la cantidad (2026-11-26): el JavaScript está conectado
// en las tres pantallas. El comportamiento se prueba en el navegador.
func TestQuantityUnitWired(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, name string, data map[string]any) string {
		t.Helper()
		data["Can"] = adminCan
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", name, data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	options := render(t, "picker_product_options.gohtml", map[string]any{"Products": []*model.Product{
		{ID: 1, Name: "QUESO", SaleType: model.SaleTypeWeight}, {ID: 2, Name: "SODA", SaleType: model.SaleTypeUnit}}})
	if !strings.Contains(options, `data-id="1" data-name="QUESO" data-batches="false" data-weight="true"`) ||
		!strings.Contains(options, `data-weight="false"`) {
		t.Errorf("options: %s", options)
	}
	script := render(t, "picker_script.gohtml", map[string]any{})
	for _, s := range []string{"function pickerSetUnit(row, product)", "weight: button.dataset.weight === 'true'",
		"htmx:confirm", "data-heavy-check", "e.detail.issueRequest(true)", "500 g = 0,5"} {
		if !strings.Contains(script, s) {
			t.Errorf("script: missing %q", s)
		}
	}
	unit := `data-qty-unit data-kg="en kg (500 g = 0,5)" data-units="unidades enteras"`
	for name, data := range map[string]map[string]any{
		"adjustment_form.gohtml": {"Form": adjustmentForm{}, "Reasons": model.ExitReasons, "Product": map[string]any{}},
		"purchase_line.gohtml":   {},
	} {
		if html := render(t, name, data); !strings.Contains(html, unit) {
			t.Errorf("%s: missing the unit", name)
		}
	}
	// Abierta desde "Dar de baja" con un producto por peso: la unidad ya
	// viene puesta y el aviso de los 50 kg funciona sin elegir de nuevo.
	html := render(t, "adjustment_form.gohtml", map[string]any{"Form": adjustmentForm{ProductID: "3", Unit: true, Weight: true},
		"Reasons": model.ExitReasons, "Product": map[string]any{}})
	if !strings.Contains(html, `inputmode="decimal"`) || !strings.Contains(html, `data-weight="1"`) ||
		!strings.Contains(html, `muted">en kg (500 g = 0,5)</span>`) {
		t.Errorf("prefilled weight: %s", html)
	}
	for _, page := range []string{"counts.gohtml", "adjustments.gohtml", "purchases.gohtml"} {
		src, err := templateFS.ReadFile("templates/pages/" + page)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), "pickerSetUnit(row, product);") {
			t.Errorf("%s: pickerSetProduct does not call pickerSetUnit", page)
		}
	}
}
