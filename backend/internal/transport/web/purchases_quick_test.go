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
	got, err := productChosenTrigger(&model.Product{ID: 42, Name: `QUESO "CREMOSO"`, ControlsBatches: true})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"purchaseProductChosen":{"batches":true,"id":42,"name":"QUESO \"CREMOSO\""}}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// El alta desde una compra es el formulario de Productos en modo compra.
func TestRenderProductFormForPurchase(t *testing.T) {
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
			"SaleTypes":  saleTypeOptions(""), "MeasureUnits": model.MeasureUnits,
		}
	}

	t.Run("scanned code: focus on the name", func(t *testing.T) {
		data := base()
		data["ForPurchase"], data["Barcode"], data["Focus"] = true, "7790895001611", "name"
		html := render(t, "product_form.gohtml", data)
		for _, s := range []string{`hx-post="/purchases/quick-product"`, `value="7790895001611"`,
			`/products/barcode-check?for=purchase`, "purchaseSwitchToAssign()", "+ Nueva categoría",
			`value="1" selected`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if strings.Contains(html, `hx-post="/products/new"`) {
			t.Error("must not save to /products/new (it redirects out of the purchase)")
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
		data["ForPurchase"], data["Focus"] = true, "category"
		html := render(t, "product_form.gohtml", data)
		if !regexpMatch(`id="product-category"[^>]*autofocus`, html) {
			t.Error("the category should have the focus")
		}
		// Sin código no hay a qué producto agregárselo.
		if strings.Contains(html, "purchaseSwitchToAssign") {
			t.Error("without a code it should not offer to assign it")
		}
	})

	t.Run("products page is unchanged", func(t *testing.T) {
		html := render(t, "product_form.gohtml", base())
		if !strings.Contains(html, `hx-post="/products/new"`) || strings.Contains(html, "?for=purchase") ||
			!regexpMatch(`id="product-barcode"[^>]*autofocus`, html) {
			t.Errorf("products page form changed: %s", html)
		}
	})

	t.Run("barcode taken: use it from a purchase, edit it from products", func(t *testing.T) {
		owner := &model.Product{ID: 3, Name: "MORTADELA", ControlsBatches: true}
		use := render(t, "product_barcode_check.gohtml", map[string]any{"ForPurchase": true,
			"Message": "Ya es de MORTADELA", "OwnerID": int64(3), "Owner": owner})
		for _, s := range []string{`data-id="3"`, `data-name="MORTADELA"`, `data-batches="true"`,
			"purchaseUseExisting(this)", "Usar ese producto en el renglón"} {
			if !strings.Contains(use, s) {
				t.Errorf("purchase: missing %q", s)
			}
		}
		if strings.Contains(use, "/products/3/edit") {
			t.Error("purchase: must not offer to edit (it leaves the purchase)")
		}
		// El dueño no se puede recibir (inactivo o combo): solo el aviso.
		none := render(t, "product_barcode_check.gohtml", map[string]any{"ForPurchase": true,
			"Message": "Ya es de COMBO", "OwnerID": int64(9)})
		if strings.Contains(none, "<button") {
			t.Errorf("purchase, owner not receivable: got %s", none)
		}
		edit := render(t, "product_barcode_check.gohtml", map[string]any{"Message": "Ya es de MORTADELA", "OwnerID": int64(3)})
		if !strings.Contains(edit, `hx-get="/products/3/edit"`) || strings.Contains(edit, "purchaseUseExisting") {
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

	panel := render(t, "purchase_assign_code.gohtml", map[string]any{"Code": "7790000000017",
		"Error": "Ese código de barras ya es del producto 000002 SODA COLA 1.5L."})
	for _, s := range []string{"Agregar código a un producto", `role="alert"`, "000002 SODA COLA 1.5L",
		`hx-get="/purchases/assign-code/options"`, `hx-include="#assign-code-value"`,
		`id="assign-code-value" name="code" value="7790000000017"`} {
		if !strings.Contains(panel, s) {
			t.Errorf("panel: missing %q", s)
		}
	}
	// El buscador no va en un <form>: Enter (el escáner) no envía nada.
	if strings.Contains(panel, "<form") {
		t.Error("panel: the search must not be inside a form")
	}

	opts := render(t, "purchase_assign_code_options.gohtml", map[string]any{"Query": "soda", "Code": "7790000000017",
		"Products": []*model.Product{{ID: 2, SKU: "000002", Name: "SODA COLA 1.5L"}}})
	for _, s := range []string{`hx-post="/purchases/assign-code"`, `name="code" value="7790000000017"`,
		`name="product_id" value="2"`, "SODA COLA 1.5L"} {
		if !strings.Contains(opts, s) {
			t.Errorf("options: missing %q", s)
		}
	}
	none := render(t, "purchase_assign_code_options.gohtml", map[string]any{"Query": "zzz", "Products": []*model.Product{}})
	if !strings.Contains(none, "No hay productos activos") {
		t.Errorf("options: no results: got %s", none)
	}
}

func regexpMatch(pattern, s string) bool { return regexp.MustCompile(pattern).MatchString(s) }
