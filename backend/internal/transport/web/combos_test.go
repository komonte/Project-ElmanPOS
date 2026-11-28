package web

import (
	"bytes"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sales"
)

// Combos (F-05): la sección de componentes, la búsqueda y los errores de la
// caja.

func TestRenderProductComponents(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, name string, can map[string]bool, data map[string]any) string {
		t.Helper()
		data["Can"] = can
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", name, data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	mustHave := func(t *testing.T, html string, want ...string) {
		t.Helper()
		for _, s := range want {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	}
	comps := []*model.ProductComponent{
		{ID: 3, PackID: 9, ComponentID: 10, Quantity: decimal.RequireFromString("20"), ComponentSKU: "000010",
			ComponentName: "CIGARRILLO SUELTO", ComponentSaleType: model.SaleTypeUnit, ComponentActive: true},
		{ID: 4, PackID: 9, ComponentID: 11, Quantity: decimal.RequireFromString("0.25"), ComponentSKU: "000011",
			ComponentName: "QUESO", ComponentSaleType: model.SaleTypeWeight, ComponentActive: false},
	}

	t.Run("admin: list, remove and add", func(t *testing.T) {
		html := render(t, "product_components.gohtml", adminCan, map[string]any{"ProductID": int64(9), "Components": comps})
		mustHave(t, html, `id="product-components"`, "× 20", "CIGARRILLO SUELTO", "× 0,25 kg", "QUESO",
			"desactivado", `hx-delete="/products/9/components/3"`, `hx-post="/products/9/components"`,
			`hx-get="/products/9/components/search"`, `name="quantity"`)
		if strings.Contains(html, `role="alert"`) {
			t.Error("plain list: unexpected alert")
		}
	})
	t.Run("stock clerk: only the list", func(t *testing.T) {
		html := render(t, "product_components.gohtml", stockCan, map[string]any{"ProductID": int64(9), "Components": comps})
		mustHave(t, html, "CIGARRILLO SUELTO", "Solo un administrador")
		if strings.Contains(html, "hx-delete") || strings.Contains(html, "hx-post") {
			t.Error("the stock clerk must not change the recipe")
		}
	})
	t.Run("empty combo and an error keep the quantity", func(t *testing.T) {
		html := render(t, "product_components.gohtml", adminCan, map[string]any{"ProductID": int64(9),
			"ComponentError": "Un combo no puede ir dentro de otro combo.", "QuantityValue": "20"})
		mustHave(t, html, "todavía no tiene componentes", `role="alert"`, "dentro de otro combo", `value="20"`)
	})
	t.Run("search options are buttons of the form", func(t *testing.T) {
		html := render(t, "product_component_options.gohtml", adminCan, map[string]any{"Query": "sue",
			"Options": []*model.Product{{ID: 10, SKU: "000010", Name: "CIGARRILLO SUELTO", SaleType: model.SaleTypeUnit}}})
		mustHave(t, html, `type="submit" name="component_id" value="10"`, "Agregar CIGARRILLO SUELTO", "Por unidad")
		none := render(t, "product_component_options.gohtml", adminCan, map[string]any{"Query": "zzz"})
		mustHave(t, none, "No hay productos")
	})
	t.Run("edit form shows the section only for a combo", func(t *testing.T) {
		data := func(st model.SaleType) map[string]any {
			return map[string]any{"Product": &model.Product{ID: 9, Name: "ATADO", SaleType: st},
				"SaleTypes": model.SaleTypes, "MeasureUnits": model.MeasureUnits, "Components": comps}
		}
		mustHave(t, render(t, "product_form.gohtml", adminCan, data(model.SaleTypeComposite)), `id="product-components"`)
		if strings.Contains(render(t, "product_form.gohtml", adminCan, data(model.SaleTypeUnit)), `id="product-components"`) {
			t.Error("a product that is not a combo has no components section")
		}
	})
}

func TestPosErrCombo(t *testing.T) {
	h := &WebHandler{}
	r := httptest.NewRequest("POST", "/pos/checkout", nil)
	short := &sales.LineError{ProductName: "ATADO", Component: "CIGARRILLO SUELTO", Err: sales.ErrNotEnoughStock,
		Available: decimal.RequireFromString("12")}
	if got, want := h.posErrText(r, "es", short), "ATADO: CIGARRILLO SUELTO: no hay stock suficiente (hay 12)."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	empty := &sales.LineError{ProductName: "ATADO", Err: sales.ErrComboEmpty}
	if got := h.posErrText(r, "es", empty); !strings.HasPrefix(got, "ATADO: es un combo sin componentes") {
		t.Errorf("empty combo: got %q", got)
	}
	inactive := &sales.LineError{ProductName: "ATADO", Component: "CIGARRILLO SUELTO",
		Err: errors.Join(sales.ErrComboComponent, model.ErrComponentInactive)}
	if got := h.posErrText(r, "es", inactive); !strings.HasPrefix(got, "ATADO: CIGARRILLO SUELTO: este componente ya no") {
		t.Errorf("inactive component: got %q", got)
	}
}
