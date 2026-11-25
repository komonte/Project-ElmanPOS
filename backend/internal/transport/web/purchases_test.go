package web

import (
	"bytes"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
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
			`name="product_q"`, `name="batch_number"`, "Confirmar recepción"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})

	t.Run("error keeps what was typed and marks the line", func(t *testing.T) {
		html := render(t, "purchase_form.gohtml", map[string]any{
			"Suppliers":     []productOption{{ID: 1, Label: "Distribuidora", Selected: true}},
			"VoucherNumber": "A-1", "Total": "20800",
			"Error": "Renglón 2: Este producto controla lotes: falta el número de lote.",
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
		html := render(t, "purchase_product_options.gohtml", map[string]any{"Query": "que",
			"Products": []*model.Product{{ID: 3, SKU: "000003", Name: "QUESO", ControlsBatches: true}}})
		for _, s := range []string{`data-id="3"`, `data-name="QUESO"`, `data-batches="true"`, "con lote"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		empty := render(t, "purchase_product_options.gohtml", map[string]any{"Query": "zzz", "Products": []*model.Product{}})
		if !strings.Contains(empty, "No hay productos activos") {
			t.Errorf("no results: got %s", empty)
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

	t.Run("page", func(t *testing.T) {
		html := render(t, "purchases.gohtml", map[string]any{"Section": "purchases", "TitleKey": "purchase.title",
			"Suppliers": []productOption{}})
		for _, s := range []string{`id="purchase-line-template"`, "function purchaseAddLine", `aria-current="page"`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})
}
