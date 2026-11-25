package web

import (
	"bytes"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

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
		got := quickProductPrefill(tt.q)
		if got.Name != tt.wantName || got.Barcode != tt.wantCode {
			t.Errorf("prefill(%q) = name %q, code %q; want %q, %q", tt.q, got.Name, got.Barcode, tt.wantName, tt.wantCode)
		}
	}
}

func TestQuickProductFromForm(t *testing.T) {
	read := func(v url.Values) (quickProduct, *model.Product, string) {
		r := httptest.NewRequest("POST", "/purchases/quick-product", strings.NewReader(v.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return quickProductFromForm(r)
	}

	qp, p, errKey := read(url.Values{"name": {"Queso cremoso"}, "category_id": {"4"}, "barcode": {" 123 "},
		"sold_by": {"kg"}, "controls_batches": {"1"}, "margin_percent": {"35,5"}})
	if errKey != "" {
		t.Fatalf("errKey = %q", errKey)
	}
	if p.SaleType != model.SaleTypeWeight || p.MeasureUnit != model.MeasureUnitKilogram || !p.ControlsBatches ||
		p.CategoryID != 4 || !p.MarginPercent.Equal(decimal.RequireFromString("35.5")) {
		t.Errorf("by kg: got %+v", p)
	}
	if qp.Barcode != "123" || !qp.ByWeight {
		t.Errorf("typed: got %+v", qp)
	}

	// Por unidad (o sin elegir), margen vacío = 0.
	_, p, errKey = read(url.Values{"name": {"Alfajor"}, "category_id": {"1"}, "sold_by": {"unit"}})
	if errKey != "" || p.SaleType != model.SaleTypeUnit || p.MeasureUnit != model.MeasureUnitUnit ||
		p.ControlsBatches || !p.MarginPercent.IsZero() {
		t.Errorf("by unit: got %+v, %q", p, errKey)
	}

	// Margen mal escrito: vuelve con lo escrito y el error.
	qp, _, errKey = read(url.Values{"name": {"Alfajor"}, "margin_percent": {"treinta"}})
	if errKey != "error.product_margin" || qp.Margin != "treinta" || qp.Name != "Alfajor" {
		t.Errorf("bad margin: got %q, %+v", errKey, qp)
	}
}

func TestQuickProductTrigger(t *testing.T) {
	got, err := quickProductTrigger(&model.Product{ID: 42, Name: `QUESO "CREMOSO"`, ControlsBatches: true})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"purchaseProductChosen":{"batches":true,"id":42,"name":"QUESO \"CREMOSO\""}}`
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestRenderQuickProduct(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, data map[string]any) string {
		t.Helper()
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", "purchase_quick_product.gohtml", data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	cats := []productOption{{ID: 4, Label: "Almacén > Lácteos"}}

	t.Run("scanned code: focus on the name", func(t *testing.T) {
		html := render(t, map[string]any{"Product": quickProductPrefill("7790895001611"), "Categories": cats})
		for _, s := range []string{`hx-post="/purchases/quick-product"`, `name="barcode"`, `value="7790895001611"`,
			"Almacén &gt; Lácteos", `value="unit" checked`, "Crear y usar en el renglón", "purchaseSwitchToAssign()"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if !regexpMatch(`name="name" required autocomplete="off" value=""\s+autofocus`, html) {
			t.Error("the name should have the focus")
		}
	})

	t.Run("typed name: focus on the category", func(t *testing.T) {
		html := render(t, map[string]any{"Product": quickProductPrefill("queso cremoso"), "Categories": cats})
		if !strings.Contains(html, `value="queso cremoso"`) || !strings.Contains(html, `name="category_id" required autofocus`) {
			t.Errorf("got %s", html)
		}
		// Sin código no hay a qué producto agregárselo.
		if strings.Contains(html, "purchaseSwitchToAssign") {
			t.Error("a typed name should not offer to assign a code")
		}
	})

	t.Run("error keeps what was typed", func(t *testing.T) {
		cats := []productOption{{ID: 4, Label: "Lácteos", Selected: true}}
		html := render(t, map[string]any{"Categories": cats, "Error": "El código ya es de 000003 MORTADELA.",
			"Product": quickProduct{Name: "QUESO", Barcode: "123", Margin: "35", CategoryID: 4, ByWeight: true, ControlsBatches: true}})
		for _, s := range []string{`role="alert"`, "000003 MORTADELA", `value="QUESO"`, `value="35"`,
			`value="4" selected`, `value="kg" checked`, `name="controls_batches" value="1" checked`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})
}

func regexpMatch(pattern, s string) bool { return regexp.MustCompile(pattern).MatchString(s) }

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
