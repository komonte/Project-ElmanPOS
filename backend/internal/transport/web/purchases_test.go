package web

import (
	"bytes"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/purchases"
)

// purchaseRequest lee los renglones de un POST con el formulario de compra.
// Los renglones van como listas: una posición por renglón en cada campo.
func purchaseRequest(t *testing.T, v url.Values) []*purchaseLine {
	t.Helper()
	r := httptest.NewRequest("POST", "/purchases", strings.NewReader(v.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	return purchaseLinesFromForm(r)
}

func TestPurchaseLinesFromForm(t *testing.T) {
	v := url.Values{
		"product_q":    {"OREO", "", "QUESO"},
		"product_id":   {"1", "", "3"},
		"quantity":     {"12", "", " 2,5 "},
		"unit_cost":    {"950", "", "4700"},
		"batch_number": {"", "", "L-9001"},
		"expiry_date":  {"", "", "2027-01-15"},
		"batches":      {"", "", "1"},
	}
	lines := purchaseRequest(t, v)
	// El renglón del medio está vacío: se descarta.
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %+v", len(lines), lines)
	}
	if l := lines[1]; l.ProductID != "3" || l.Quantity != "2,5" || l.BatchNumber != "L-9001" ||
		l.ExpiryDate != "2027-01-15" || !l.Batches {
		t.Errorf("line 2: got %+v", l)
	}
	if lines[0].Batches {
		t.Error("line 1: batches should be false")
	}
}

func TestPurchaseFromForm(t *testing.T) {
	lines := []*purchaseLine{
		{ProductID: "1", Quantity: "12", UnitCost: "950"},
		{ProductID: "3", Quantity: "2,5", UnitCost: "4700,50", BatchNumber: "L-1", ExpiryDate: "2027-01-15"},
	}
	r := httptest.NewRequest("POST", "/purchases", strings.NewReader("total=23151,25&voucher_number=A-1"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	p, err := purchaseFromForm(r, 7, lines)
	if err != nil {
		t.Fatal(err)
	}
	if p.SupplierID != 7 || p.VoucherNumber != "A-1" || !p.Total.Equal(decimal.RequireFromString("23151.25")) ||
		len(p.Items) != 2 {
		t.Fatalf("got %+v", p)
	}
	it := p.Items[1]
	if it.ProductID != 3 || !it.Quantity.Equal(decimal.RequireFromString("2.5")) ||
		!it.UnitCost.Equal(decimal.RequireFromString("4700.5")) || it.BatchNumber != "L-1" {
		t.Errorf("line 2: got %+v", it)
	}

	// Números mal escritos: error del modelo con su renglón.
	tests := []struct {
		name    string
		total   string
		line    purchaseLine
		wantErr error
		wantN   int
	}{
		{"bad total", "mil", purchaseLine{ProductID: "1", Quantity: "1", UnitCost: "1"}, model.ErrInvalidPurchaseTotal, 0},
		{"empty quantity", "1", purchaseLine{ProductID: "1", UnitCost: "1"}, model.ErrInvalidPurchaseQuantity, 2},
		{"bad cost", "1", purchaseLine{ProductID: "1", Quantity: "1", UnitCost: "1.2.3"}, model.ErrInvalidCost, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/purchases", strings.NewReader("total="+tt.total))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			l := tt.line
			_, err := purchaseFromForm(r, 1, []*purchaseLine{lines[0], &l})
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("got %v, want %v", err, tt.wantErr)
			}
			if n := errorLine(err); n != tt.wantN {
				t.Errorf("line: got %d, want %d", n, tt.wantN)
			}
		})
	}
}

func TestErrorLine(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{errors.New("supplier is inactive"), 0},
		{fmt.Errorf("line 3: %w", purchases.ErrProductInactive), 3},
		// Validate dentro del caso de uso: "validation failed: line 12: ...".
		{fmt.Errorf("%w: %w", service.ErrValidation, fmt.Errorf("line 12: %w", model.ErrInvalidPurchaseQuantity)), 12},
		// "deadline" no es "line".
		{errors.New("deadline 5: exceeded"), 0},
	}
	for _, tt := range tests {
		if got := errorLine(tt.err); got != tt.want {
			t.Errorf("errorLine(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}

func TestRenderPurchaseForm(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, name string, data map[string]any) string {
		t.Helper()
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", name, data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	t.Run("empty form has one line", func(t *testing.T) {
		html := render(t, "purchase_form.gohtml", map[string]any{
			"Suppliers": []productOption{{ID: 1, Label: "Distribuidora del Sur S.A."}}})
		if n := strings.Count(html, "<fieldset data-purchase-line"); n != 1 {
			t.Errorf("lines: got %d, want 1", n)
		}
		for _, s := range []string{`hx-post="/purchases"`, "Distribuidora del Sur S.A.", "Total sin IVA",
			`name="product_q"`, `name="batch_number"`, "Vacío: C-&lt;n° de compra&gt;", "Confirmar recepción"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})

	t.Run("error keeps what was typed and marks the line", func(t *testing.T) {
		html := render(t, "purchase_form.gohtml", map[string]any{
			"Suppliers":     []productOption{{ID: 1, Label: "Distribuidora", Selected: true}},
			"VoucherNumber": "A-1", "Total": "20800",
			"Error": "Renglón 2: Este producto no controla lotes: dejá lote y vencimiento vacíos.",
			"Lines": []*purchaseLine{
				{ProductID: "1", ProductQ: "OREO", Quantity: "12", UnitCost: "950"},
				{ProductID: "3", ProductQ: "QUESO", Quantity: "2,5", UnitCost: "4700", Batches: true, Error: true},
			}})
		for _, s := range []string{`role="alert"`, "Renglón 2:", `value="A-1"`, `value="20800"`,
			`value="OREO"`, `value="2,5"`, `selected`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if n := strings.Count(html, "border-danger"); n < 2 { // el aviso y el renglón
			t.Errorf("marked: got %d border-danger, want the alert and line 2", n)
		}
		// Lote visible solo en el renglón que controla lotes.
		if n := strings.Count(html, "data-batch-fields class=\"mt-3 grid gap-3 md:grid-cols-2\" hidden"); n != 1 {
			t.Errorf("hidden batch fields: got %d, want 1 (line 1)", n)
		}
	})

	t.Run("product options", func(t *testing.T) {
		html := render(t, "picker_product_options.gohtml", map[string]any{"Query": "que",
			"Products": []*model.Product{{ID: 3, SKU: "000003", Name: "QUESO", ControlsBatches: true, ShelfLifeDays: new(21)},
				{ID: 4, SKU: "000004", Name: "QUESO RALLADO"}}})
		for _, s := range []string{`data-id="3"`, `data-name="QUESO"`, `data-batches="true"`, `data-shelf-life="21"`,
			`data-shelf-life=""`, "con lote"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		empty := render(t, "picker_product_options.gohtml", map[string]any{"Query": "zzz", "Products": []*model.Product{}})
		for _, s := range []string{"No hay productos activos", `data-quick-product data-q="zzz"`,
			"Dar de alta «zzz»", "pickerQuickProduct(this)"} {
			if !strings.Contains(empty, s) {
				t.Errorf("no results: missing %q in %s", s, empty)
			}
		}
		// "zzz" no es un código: no se ofrece agregarlo a otro producto.
		if strings.Contains(empty, "pickerAssignCode") {
			t.Error("a name should not offer to assign a code")
		}
		code := render(t, "picker_product_options.gohtml", map[string]any{"Query": "7790000000017",
			"IsCode": true, "Products": []*model.Product{}})
		for _, s := range []string{"Dar de alta «7790000000017»", `data-q="7790000000017" onclick="pickerAssignCode(this)"`,
			"Agregar este código a un producto existente"} {
			if !strings.Contains(code, s) {
				t.Errorf("code: missing %q", s)
			}
		}
		// Con resultados no se ofrece el alta.
		if strings.Contains(html, "data-quick-product") {
			t.Error("results: should not offer a quick create")
		}
	})

	t.Run("received with a difference", func(t *testing.T) {
		html := render(t, "purchase_received.gohtml", map[string]any{
			"Purchase":   &model.Purchase{ID: 377, Items: make([]*model.PurchaseItem, 2)},
			"Difference": decimal.RequireFromString("-50"), "HasDifference": true})
		for _, s := range []string{"Compra recibida", "Compra N° 377 · 2 renglones", "-$ 50,00", `href="/purchases"`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})

	t.Run("received: price it on the invoice", func(t *testing.T) {
		html := render(t, "purchase_received.gohtml", map[string]any{
			"Purchase": &model.Purchase{ID: 377, Items: make([]*model.PurchaseItem, 3)}, "Products": 2})
		for _, s := range []string{`id="purchase-price-it" href="/purchases/377"`, "Ver la factura y poner precios",
			"Llegaron 2 productos"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})

	t.Run("received: count what was sold without stock first", func(t *testing.T) {
		html := render(t, "purchase_received.gohtml", map[string]any{
			"Purchase":  &model.Purchase{ID: 378, Items: make([]*model.PurchaseItem, 2)},
			"Unstocked": []model.UnstockedItem{{ProductID: 5, Name: "OREO", Uncovered: decimal.RequireFromString("3")}}})
		for _, s := range []string{`id="purchase-unstocked"`, "Antes de acomodar", `data-product="5"`, "OREO",
			"3 vendido sin stock", `href="/counts"`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		plain := render(t, "purchase_received.gohtml", map[string]any{
			"Purchase": &model.Purchase{ID: 379, Items: make([]*model.PurchaseItem, 1)}})
		if strings.Contains(plain, `id="purchase-unstocked"`) {
			t.Error("warning without products sold without stock")
		}
	})

	t.Run("product options mark what was sold without stock", func(t *testing.T) {
		html := render(t, "picker_product_options.gohtml", map[string]any{"Query": "oreo",
			"Products":  []*model.Product{{ID: 5, SKU: "000005", Name: "OREO"}, {ID: 6, SKU: "000006", Name: "OREO MINI"}},
			"Unstocked": map[int64]string{5: "3"}})
		if n := strings.Count(html, "data-unstocked"); n != 1 {
			t.Errorf("marks: got %d, want 1", n)
		}
		if !strings.Contains(html, "Vendido sin stock: 3.") {
			t.Error("missing the mark text")
		}
	})

	t.Run("page", func(t *testing.T) {
		html := render(t, "purchases.gohtml", map[string]any{"Section": "purchases", "TitleKey": "purchase.title",
			"Suppliers": []productOption{}, "Can": adminCan})
		for _, s := range []string{`id="purchase-line-template"`, "function purchaseAddLine", `aria-current="page"`,
			`<div id="form-modal"></div>`, "function purchaseExpiryFrom",
			// El buscador compartido (picker_script.gohtml) y lo que la compra
			// hace con el producto elegido.
			"function pickerQuickProduct", "addEventListener('productChosen'", "function pickerAssignCode",
			"function pickerSwitchToAssign", "function pickerUseExisting", "function pickerSetProduct",
			"function pickerQuickValues"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})
}

// El formulario de compra tiene hx-swap="outerHTML" y htmx lo hereda. Si el
// buscador de un renglón no dice su propio swap, cada respuesta reemplaza la
// lista entera: el <ul> desaparece, elegir un producto no la vacía y la
// búsqueda siguiente escribe en la lista del renglón de abajo ("next ul").
func TestPurchaseLineSearchSwapsInsideItsList(t *testing.T) {
	var buf bytes.Buffer
	if err := NewTemplateEngine().Render(&buf, "es", "purchase_line.gohtml", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	input := regexp.MustCompile(`(?s)<input type="search" name="product_q".*?>`).FindString(buf.String())
	if input == "" {
		t.Fatal("product_q input not found")
	}
	if !strings.Contains(input, `hx-swap="innerHTML"`) {
		t.Errorf(`product_q must set hx-swap="innerHTML", got %s`, input)
	}
}

// Si se elige un producto antes de que responda la última búsqueda, esa
// respuesta llega después y no tiene que volver a abrir la lista. Lo hace el
// buscador compartido (pickerDropLateSearch, en htmx:beforeSwap); este test
// solo revisa que esté registrado: el comportamiento se probó en un navegador.
func TestPurchasesPageDropsLateSearches(t *testing.T) {
	var buf bytes.Buffer
	if err := NewTemplateEngine().Render(&buf, "es", "purchases.gohtml", map[string]any{
		"Section": "purchases", "TitleKey": "purchase.title", "Suppliers": []productOption{}}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"function pickerDropLateSearch",
		"addEventListener('htmx:beforeSwap', pickerDropLateSearch)"} {
		if !strings.Contains(buf.String(), s) {
			t.Errorf("missing %q", s)
		}
	}
}

// El lector de códigos manda Enter después del código. En el buscador de un
// renglón ese Enter no tiene que enviar la compra a medio cargar: lo maneja
// pickerSearchEnter (busca al momento y elige si hay un solo resultado).
// Como en TestPurchasesPageDropsLateSearches, el test revisa que esté
// conectado; el comportamiento se prueba en un navegador.
func TestPurchaseLineSearchHandlesEnter(t *testing.T) {
	e := NewTemplateEngine()
	var line, page bytes.Buffer
	if err := e.Render(&line, "es", "purchase_line.gohtml", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	input := regexp.MustCompile(`(?s)<input type="search" name="product_q".*?>`).FindString(line.String())
	if !strings.Contains(input, `onkeydown="pickerSearchEnter(event, this)"`) ||
		!strings.Contains(input, `hx-get="/picker/product-options"`) {
		t.Errorf("product_q must use the shared picker, got %s", input)
	}
	if !strings.Contains(line.String(), "<fieldset data-purchase-line data-picker-line") {
		t.Error("the line must be a picker line (data-picker-line)")
	}
	if err := e.Render(&page, "es", "purchases.gohtml", map[string]any{
		"Section": "purchases", "TitleKey": "purchase.title", "Suppliers": []productOption{}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page.String(), "function pickerSearchEnter") {
		t.Error("page must define pickerSearchEnter")
	}
}

// Al cerrar un panel abierto desde un renglón (Escape, Cancelar, la X), el
// cursor vuelve al buscador de ese renglón, para seguir escaneando sin el
// mouse. closeModal (layout) vuelve al elemento marcado con
// data-modal-return, y la página marca el buscador al abrir el panel. Como
// los otros tests de JavaScript, revisa que esté conectado; el
// comportamiento se prueba en un navegador.
func TestPurchasePanelReturnsFocusToItsLine(t *testing.T) {
	var buf bytes.Buffer
	if err := NewTemplateEngine().Render(&buf, "es", "purchases.gohtml", map[string]any{
		"Section": "purchases", "TitleKey": "purchase.title", "Suppliers": []productOption{}}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, s := range []string{
		"document.querySelector('[data-modal-return]')", // closeModal, en el layout
		"setAttribute('data-modal-return', '')",         // al abrir el panel
	} {
		if !strings.Contains(html, s) {
			t.Errorf("missing %q", s)
		}
	}
}
