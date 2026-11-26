package web

import (
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

func TestCountEntryFromForm(t *testing.T) {
	e, err := countEntryFromForm(countEntryForm{Sector: "Heladera", ProductID: "7", Quantity: "2,5"})
	if err != nil || e.ProductID != 7 || e.Sector != "Heladera" || !e.Quantity.Equal(decimal.RequireFromString("2.5")) {
		t.Errorf("valid: got %+v, %v", e, err)
	}
	if e, err := countEntryFromForm(countEntryForm{Sector: "A", ProductID: "7", Quantity: "0"}); err != nil || !e.Quantity.IsZero() {
		t.Errorf("zero: got %+v, %v", e, err)
	}
	tests := []struct {
		name    string
		form    countEntryForm
		wantErr error
	}{
		{"no product", countEntryForm{Sector: "A", ProductQ: "soda", Quantity: "1"}, model.ErrStockCountEntryProduct},
		{"empty quantity", countEntryForm{Sector: "A", ProductID: "7"}, model.ErrInvalidCountedQuantity},
		{"bad quantity", countEntryForm{Sector: "A", ProductID: "7", Quantity: "doce"}, model.ErrInvalidCountedQuantity},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := countEntryFromForm(tt.form); !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestRenderCounts(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, name string, data map[string]any) string {
		t.Helper()
		var buf bytes.Buffer
		if _, ok := data["Can"]; !ok {
			data["Can"] = adminCan
		}
		if err := e.Render(&buf, "es", name, data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	count := &model.StockCount{ID: 5, Status: model.CountDraft, Items: []*model.StockCountItem{
		{ID: 1, ProductName: "QUESO CREMOSO", SystemQuantity: decimal.RequireFromString("99"),
			CountedQuantity: decimal.RequireFromString("2.5"), Entries: []*model.StockCountEntry{
				{ID: 11, Sector: "Heladera", Quantity: decimal.RequireFromString("2.5")}}},
		{ID: 2, ProductName: "SODA COLA 1.5L", SystemQuantity: decimal.RequireFromString("77"),
			CountedQuantity: decimal.RequireFromString("18"), Entries: []*model.StockCountEntry{
				{ID: 12, Sector: "Góndola 3", Quantity: decimal.RequireFromString("6")},
				{ID: 13, Sector: "Depósito", Quantity: decimal.RequireFromString("12")}}},
	}}
	sheet := func(form countEntryForm, last *countLast, errMsg string) map[string]any {
		return map[string]any{"Count": count, "Entries": 3, "Sectors": []string{"Depósito", "Góndola 3"},
			"Form": form, "Last": last, "Error": errMsg}
	}
	focused := func(html string) string {
		m := regexp.MustCompile(`name="(\w+)"[^>]*data-focus`).FindStringSubmatch(html)
		if m == nil {
			return ""
		}
		return m[1]
	}

	t.Run("no count: start one", func(t *testing.T) {
		html := render(t, "counts.gohtml", map[string]any{"Section": "counts", "TitleKey": "count.title"})
		for _, s := range []string{`<form method="post" action="/counts">`, "Empezar un conteo",
			`href="/counts" aria-current="page"`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if strings.Contains(html, "count-sheet") || strings.Contains(html, "pickerSearchEnter") {
			t.Error("without a count there is no sheet")
		}
	})

	t.Run("draft page: sheet and shared picker", func(t *testing.T) {
		html := render(t, "counts.gohtml", map[string]any{"Section": "counts", "TitleKey": "count.title",
			"Sheet": sheet(countEntryForm{}, nil, "")})
		for _, s := range []string{`id="count-sheet"`, `<div id="form-modal"></div>`,
			"function pickerSearchEnter", "addEventListener('productChosen'", "function pickerSetProduct",
			"e.detail.target.id !== 'count-sheet'"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})

	t.Run("sheet", func(t *testing.T) {
		html := render(t, "count_sheet.gohtml", sheet(countEntryForm{Sector: "Depósito"}, nil, ""))
		for _, s := range []string{`<form id="count-entry" data-picker-line data-heavy-check hx-post="/counts/5/entries"`,
			`hx-get="/picker/product-options"`, `hx-target="next ul" hx-swap="innerHTML"`,
			`onkeydown="pickerSearchEnter(event, this)"`, `name="sector" list="count-sectors"`,
			`value="Depósito"`, `<option value="Góndola 3"></option>`,
			"QUESO CREMOSO", "2,5", "SODA COLA 1.5L", "Góndola 3:", ">18<", "2 productos, 3 cargas",
			`hx-post="/counts/5/entries/13/delete" hx-include="#count-sector"`,
			`action="/counts/5/discard"`, "return confirm("} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		// Mientras se cuenta no se muestra lo que dice el sistema.
		if strings.Contains(html, ">99<") || strings.Contains(html, ">77<") {
			t.Error("the sheet must not show the system quantity")
		}
		if got := focused(html); got != "product_q" {
			t.Errorf("with a sector: focus on %q, want product_q", got)
		}
	})

	t.Run("focus", func(t *testing.T) {
		if got := focused(render(t, "count_sheet.gohtml", sheet(countEntryForm{}, nil, ""))); got != "sector" {
			t.Errorf("no sector: focus on %q, want sector", got)
		}
		failed := render(t, "count_sheet.gohtml", sheet(countEntryForm{Sector: "A", ProductID: "7",
			ProductQ: "SODA", Quantity: "doce"}, nil, "La cantidad tiene que ser un número"))
		if got := focused(failed); got != "quantity" {
			t.Errorf("bad quantity: focus on %q, want quantity", got)
		}
		if !strings.Contains(failed, `role="alert"`) || !strings.Contains(failed, `value="doce"`) {
			t.Error("bad quantity: the error and what was typed must come back")
		}
	})

	t.Run("last entry", func(t *testing.T) {
		html := render(t, "count_sheet.gohtml", sheet(countEntryForm{Sector: "Depósito"}, &countLast{Name: "SODA COLA 1.5L",
			Sector: "Depósito", Quantity: decimal.RequireFromString("12"), Total: decimal.RequireFromString("18")}, ""))
		for _, s := range []string{`role="status"`, "Cargado: SODA COLA 1.5L × 12 en Depósito", "contado en total: 18"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})
}

// reviewCount: un conteo con un producto de cada caso.
func reviewCount(status string) *model.StockCount {
	d := decimal.RequireFromString
	cost, avg := d("50"), d("100")
	confirmedAt := "2026-11-26T10:30:00Z"
	c := &model.StockCount{ID: 7, Status: status, Items: []*model.StockCountItem{
		{ProductID: 1, ProductName: "YERBA", SystemQuantity: d("0"), CountedQuantity: d("24"), UnitCost: &cost},
		{ProductID: 2, ProductName: "FIDEOS", SystemQuantity: d("2"), CountedQuantity: d("5"), AvgCost: &avg},
		{ProductID: 3, ProductName: "QUESO", SystemQuantity: d("0"), CountedQuantity: d("1.5"), ControlsBatches: true,
			ExpiryDate: "2027-03-31"},
		{ProductID: 4, ProductName: "SALAME", SystemQuantity: d("10"), CountedQuantity: d("7"), ControlsBatches: true},
		{ProductID: 5, ProductName: "ARROZ", SystemQuantity: d("8"), CountedQuantity: d("8")},
	}}
	if status == model.CountConfirmed {
		c.ConfirmedAt = &confirmedAt
	}
	return c
}

func TestCountSummary(t *testing.T) {
	got := countSummary(reviewCount(model.CountDraft), false)
	want := countReviewSummary{Up: 3, Down: 1, Same: 1, NoCost: 2, ZeroCost: 1}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestRenderCountReview(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, name string, data map[string]any) string {
		t.Helper()
		var buf bytes.Buffer
		if _, ok := data["Can"]; !ok {
			data["Can"] = adminCan
		}
		if err := e.Render(&buf, "es", name, data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	page := func(c *model.StockCount, done bool, errMsg string) map[string]any {
		var rows []countReviewRow
		for _, it := range c.Items {
			if row := newCountReviewRow("es", c, it); !row.Diff.IsZero() {
				rows = append(rows, row)
			}
		}
		return map[string]any{"Section": "counts", "TitleKey": "count.review_title", "Count": c, "Rows": rows,
			"Summary": countSummary(c, false), "Draft": c.Status == model.CountDraft, "Done": done, "Error": errMsg,
			"BatchNumber": "I-7"}
	}

	t.Run("draft", func(t *testing.T) {
		html := render(t, "count_review.gohtml", page(reviewCount(model.CountDraft), false, ""))
		for _, s := range []string{"3 productos suben, 1 bajan y 1 coinciden",
			"2 productos suben sin costo escrito", "1 de ellos nunca tuvieron costo",
			"YERBA", "+24", `value="50"`, "SALAME", "-3", "Se descuenta de los lotes",
			"Promedio de hoy: $ 100,00", "Sin costo cargado",
			`hx-post="/counts/7/items/1/details"`, `hx-include="#count-row-1"`,
			`type="date" name="expiry_date" value="2027-03-31"`, "lote I-7",
			`action="/counts/7/confirm"`, "Cambia el stock de 4 productos", `href="/counts"`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		// Coincide: no aparece. Sin lotes no hay vencimiento; lo que baja no tiene costo.
		if strings.Contains(html, "ARROZ") {
			t.Error("a product that matches must not be listed")
		}
		if n := strings.Count(html, `name="expiry_date"`); n != 1 {
			t.Errorf("expiry inputs: got %d, want 1 (QUESO)", n)
		}
		if n := strings.Count(html, `name="unit_cost"`); n != 3 {
			t.Errorf("cost inputs: got %d, want 3 (the ones that go up)", n)
		}
	})

	t.Run("confirmed: read only", func(t *testing.T) {
		html := render(t, "count_review.gohtml", page(reviewCount(model.CountConfirmed), true, ""))
		for _, s := range []string{"Conteo confirmado", "Se ajustó el stock de 4 productos", "Conteo N° 7, confirmado el",
			"$ 50,00", "2027-03-31"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		for _, s := range []string{`name="unit_cost"`, `name="expiry_date"`, "/confirm", "/details"} {
			if strings.Contains(html, s) {
				t.Errorf("confirmed count must be read only: found %q", s)
			}
		}
	})

	t.Run("row with an error and summary out of band", func(t *testing.T) {
		c := reviewCount(model.CountDraft)
		row := newCountReviewRow("es", c, c.Items[1])
		row.CostInput, row.Error = "mil", "El costo tiene que ser mayor a 0"
		html := render(t, "count_review_row.gohtml", map[string]any{"Row": row})
		for _, s := range []string{`<tr id="count-row-2"`, `role="alert"`, `value="mil"`} {
			if !strings.Contains(html, s) {
				t.Errorf("row: missing %q", s)
			}
		}
		sum := render(t, "count_review_summary.gohtml", map[string]any{"Summary": countSummary(c, true)})
		if !strings.Contains(sum, `id="count-review-summary" hx-swap-oob="true"`) {
			t.Errorf("summary must swap out of band: %s", sum)
		}
	})
}
