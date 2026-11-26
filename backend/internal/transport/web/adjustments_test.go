package web

import (
	"bytes"
	"errors"
	"fmt"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/adjustments"
)

func TestAdjustmentFromForm(t *testing.T) {
	a, err := adjustmentFromForm(adjustmentForm{ProductID: "7", Quantity: "0,350", Reason: model.ReasonDamage,
		BatchID: "12", Notes: "se cayó"})
	if err != nil || a.ProductID != 7 || !a.Quantity.Equal(decimal.RequireFromString("0.35")) ||
		a.Reason != model.ReasonDamage || a.BatchID == nil || *a.BatchID != 12 || a.Notes != "se cayó" {
		t.Errorf("valid: got %+v, %v", a, err)
	}
	if a, _ := adjustmentFromForm(adjustmentForm{ProductID: "7", Quantity: "1"}); a.BatchID != nil {
		t.Errorf("no batch: got %v", *a.BatchID)
	}
	tests := []struct {
		name    string
		form    adjustmentForm
		wantErr error
	}{
		{"no product", adjustmentForm{ProductQ: "soda", Quantity: "1"}, model.ErrStockAdjustmentProduct},
		{"empty quantity", adjustmentForm{ProductID: "7"}, model.ErrInvalidAdjustmentQuantity},
		{"bad quantity", adjustmentForm{ProductID: "7", Quantity: "dos"}, model.ErrInvalidAdjustmentQuantity},
		{"bad batch", adjustmentForm{ProductID: "7", Quantity: "1", BatchID: "x"}, model.ErrInvalidAdjustmentBatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := adjustmentFromForm(tt.form); !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// La falta de stock dice cuánto hay, como se escribe en el idioma.
func TestAdjustmentNotEnoughMessage(t *testing.T) {
	h := &WebHandler{}
	r := httptest.NewRequest("POST", "/adjustments", nil)
	err := fmt.Errorf("wrapped: %w", &adjustments.NotEnoughStockError{Available: decimal.RequireFromString("2.5")})
	if !errors.Is(err, service.ErrConflict) {
		t.Fatal("not enough stock must be a conflict")
	}
	if got := h.errText(r, "es", err); got != "No hay tanto en stock: hay 2,5." {
		t.Errorf("got %q", got)
	}
}

func TestRenderAdjustments(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, name string, data map[string]any) string {
		t.Helper()
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", name, data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	formData := func(form adjustmentForm, product map[string]any, last *adjustmentLast, errMsg string) map[string]any {
		if product == nil {
			product = map[string]any{"BatchID": ""}
		}
		return map[string]any{"Form": form, "Reasons": model.ExitReasons, "Product": product, "Last": last, "Error": errMsg}
	}
	focused := func(html string) string {
		m := regexp.MustCompile(`name="(\w+)"[^>]*data-focus`).FindStringSubmatch(html)
		if m == nil {
			return ""
		}
		return m[1]
	}
	recent := []*model.StockAdjustment{{ProductName: "SODA COLA 1.5L", Reason: model.ReasonOwnConsumption,
		Quantity: decimal.RequireFromString("2"), Notes: "mate", CreatedAt: "2026-11-26T13:00:00Z"}}

	t.Run("page", func(t *testing.T) {
		html := render(t, "adjustments.gohtml", map[string]any{"Section": "adjustments", "TitleKey": "adjustment.title",
			"Form":   formData(adjustmentForm{Reason: model.ReasonOwnConsumption}, nil, nil, ""),
			"Recent": map[string]any{"Adjustments": recent}})
		for _, s := range []string{`href="/adjustments" aria-current="page"`,
			`<form id="adjustment-form" data-picker-line hx-post="/adjustments"`,
			`hx-get="/picker/product-options"`, `onkeydown="pickerSearchEnter(event, this)"`,
			`<option value="OWN_CONSUMPTION" selected>Consumo propio</option>`, ">Rotura<", ">Vencido<", ">Otro<",
			"function pickerSetProduct", "'/adjustments/product'", "addEventListener('productChosen'",
			`<div id="form-modal"></div>`, `id="adjustment-recent"`, "SODA COLA 1.5L", ">Consumo propio<", "mate"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if strings.Contains(html, "THEFT") || strings.Contains(html, "COUNT\"") {
			t.Error("theft and count are not exit reasons")
		}
		if got := focused(html); got != "product_q" {
			t.Errorf("focus on %q, want product_q", got)
		}
	})

	t.Run("recorded, same reason kept, warning if counted", func(t *testing.T) {
		html := render(t, "adjustment_form.gohtml", formData(adjustmentForm{Reason: model.ReasonExpiration}, nil,
			&adjustmentLast{Name: "QUESO", Reason: model.ReasonExpiration, Quantity: decimal.RequireFromString("1.25"),
				InOpenCount: true}, ""))
		for _, s := range []string{`role="status"`, "Anotado: QUESO × 1,25 (Vencido)", "ya estaba contado en el conteo abierto",
			`<option value="EXPIRATION" selected>`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})

	t.Run("error keeps what was typed", func(t *testing.T) {
		html := render(t, "adjustment_form.gohtml", formData(adjustmentForm{ProductID: "7", ProductQ: "SODA",
			Quantity: "9", Reason: model.ReasonOther, Notes: ""}, nil, nil, "No hay tanto en stock: hay 2."))
		for _, s := range []string{`role="alert"`, "hay 2.", `value="9"`, `value="SODA"`, `<option value="OTHER" selected>`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if got := focused(html); got != "quantity" {
			t.Errorf("focus on %q, want quantity", got)
		}
	})

	t.Run("product info: batches and open count", func(t *testing.T) {
		html := render(t, "adjustment_product.gohtml", map[string]any{"BatchID": "12", "InOpenCount": true,
			"Batches": []*model.ProductBatch{
				{ID: 11, BatchNumber: "L-1", ExpiryDate: "2027-01-31", RemainingQuantity: decimal.RequireFromString("2.5")},
				{ID: 12, BatchNumber: "I-5", RemainingQuantity: decimal.RequireFromString("1")}}})
		for _, s := range []string{`id="adjustment-product-info"`, "ya está contado en el conteo abierto",
			`name="batch_id"`, `<option value="">Automático: primero el que vence antes</option>`,
			"L-1 · vence 2027-01-31 · quedan 2,5", `<option value="12" selected>I-5 · vence - · quedan 1</option>`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		empty := render(t, "adjustment_product.gohtml", map[string]any{"BatchID": ""})
		if strings.Contains(empty, "<select") || strings.Contains(empty, `role="status"`) {
			t.Errorf("no product: got %s", empty)
		}
	})

	t.Run("recent out of band and empty", func(t *testing.T) {
		oob := render(t, "adjustment_recent.gohtml", map[string]any{"Adjustments": recent, "OOB": true})
		if !strings.Contains(oob, `id="adjustment-recent" hx-swap-oob="true"`) {
			t.Errorf("recent must swap out of band: %s", oob)
		}
		none := render(t, "adjustment_recent.gohtml", map[string]any{"Adjustments": []*model.StockAdjustment{}})
		if !strings.Contains(none, "Todavía no se anotó ninguna salida") {
			t.Errorf("empty: got %s", none)
		}
	})
}
