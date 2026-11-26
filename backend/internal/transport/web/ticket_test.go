package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sales"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// ticketSale es una venta como la devuelve sales.Get: 2 sodas y 0,35 kg de
// mortadela, pagada en efectivo y transferencia.
func ticketSale() *model.Sale {
	return &model.Sale{
		ID: 7, SaleNumber: 12, Status: model.SaleCompleted, CreatedAt: "2026-11-26T14:05:00Z",
		Total: dec("12450"), TaxAmount: dec("2160.74"),
		RegisterName: "Caja 1", StoreName: "Almacén Elman", StoreAddress: "Calle 123",
		Items: []*model.SaleItem{
			{ProductName: "SODA COLA 1.5L", SaleType: model.SaleTypeUnit, Quantity: dec("2"), UnitPrice: dec("5000")},
			{ProductName: "MORTADELA", SaleType: model.SaleTypeWeight, Quantity: dec("0.350"), UnitPrice: dec("7000")},
		},
		Payments: []*model.Payment{
			{Method: model.PaymentCash, Amount: dec("10450")},
			{Method: model.PaymentTransfer, Amount: dec("2000"), Reference: "op 77"},
		},
	}
}

func TestTicketCash(t *testing.T) {
	tests := []struct {
		name, raw string
		ok        bool
		change    string
	}{
		{"recién cobrada", "20000", true, "9550"},
		{"con centavos", "10450.5", true, "0.5"},
		{"reimpreso, sin paid", "", false, ""},
		{"justo, sin vuelto", "10450", false, ""},
		{"menos que el efectivo", "100", false, ""},
		{"mal escrito", "veinte", false, ""},
		{"con miles a mano: no es el formato", "20.000", false, ""},
		{"tres decimales", "20000.123", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, change, ok := ticketCash(ticketSale(), tt.raw)
			if ok != tt.ok || (ok && !change.Equal(dec(tt.change))) {
				t.Errorf("ticketCash(%q) = %s, %v; want %s, %v", tt.raw, change, ok, tt.change, tt.ok)
			}
		})
	}

	noCash := ticketSale()
	noCash.Payments = noCash.Payments[1:]
	if _, _, ok := ticketCash(noCash, "20000"); ok {
		t.Error("a sale without cash has no change")
	}
}

func TestTicketURL(t *testing.T) {
	sale := ticketSale()
	sale.Payments[0].Tendered = dec("20000")
	sale.Change = dec("9550")
	if got := ticketURL(sale); got != "/pos/sales/7/ticket?paid=20000" {
		t.Errorf("with change: got %q", got)
	}
	sale.Payments[0].Tendered, sale.Change = dec("10450.5"), dec("0.5")
	if got := ticketURL(sale); got != "/pos/sales/7/ticket?paid=10450.5" {
		t.Errorf("with cents: got %q", got)
	}
	// Lo que escribe ticketURL lo lee ticketCash.
	if _, change, ok := ticketCash(sale, strings.TrimPrefix(ticketURL(sale), "/pos/sales/7/ticket?paid=")); !ok ||
		!change.Equal(dec("0.5")) {
		t.Errorf("round trip: got %s, %v", change, ok)
	}
	sale.Change = decimal.Zero
	if got := ticketURL(sale); got != "/pos/sales/7/ticket" {
		t.Errorf("without change: got %q", got)
	}
}

func TestRenderTicket(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, data map[string]any) string {
		t.Helper()
		data["Lang"] = "es"
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", "ticket.gohtml", data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	html := render(t, map[string]any{"Sale": ticketSale(), "Paid": dec("20000"), "Change": dec("9550")})
	for _, s := range []string{
		// Documento entero, sin el menú, a 80 mm.
		"<!DOCTYPE html>", `<style id="ticket-page">@page { size: 80mm 297mm; margin: 0; }</style>`,
		"'@page { size: 80mm ' + mm + 'mm; margin: 0; }'", "@media print", "width: 72mm",
		"Almacén Elman", "Calle 123", "Caja 1", "Venta N.º 12", "2026-11-26 11:05:00",
		"SODA COLA 1.5L", "2 × $ 5.000,00", "$ 10.000,00",
		"MORTADELA", "0,35 kg × $ 7.000,00/kg", "$ 2.450,00",
		"TOTAL", "$ 12.450,00", "IVA incluido", "$ 2.160,74",
		"Efectivo", "$ 10.450,00", "Transferencia (op 77)", "$ 2.000,00",
		"Paga con", "$ 20.000,00", "Vuelto", "$ 9.550,00",
		"No válido como factura", `onclick="window.print()"`, `href="/pos"`,
	} {
		if !strings.Contains(html, s) {
			t.Errorf("missing %q", s)
		}
	}
	if strings.Contains(html, "<nav") || strings.Contains(html, "output.css") {
		t.Error("the ticket must not carry the layout")
	}
	if strings.Contains(html, "data-voided") {
		t.Error("a completed sale is not voided")
	}

	reprint := render(t, map[string]any{"Sale": ticketSale()})
	if strings.Contains(reprint, "data-cash-change") || strings.Contains(reprint, "Vuelto") {
		t.Error("a reprinted ticket has no change")
	}

	voided, noAddress := ticketSale(), ticketSale()
	voided.Status = model.SaleVoided
	if html := render(t, map[string]any{"Sale": voided}); !strings.Contains(html, "data-voided") ||
		!strings.Contains(html, "ANULADA") {
		t.Error("a voided sale must say so")
	}
	noAddress.StoreAddress = ""
	if html := render(t, map[string]any{"Sale": noAddress}); strings.Contains(html, `<div class="muted"></div>`) {
		t.Error("no address, no empty line")
	}
}

func TestPosTicketNotFound(t *testing.T) {
	h := &WebHandler{engine: NewTemplateEngine(), sales: sales.New(nil, nil)}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/pos/sales/abc/ticket", nil)
	r.SetPathValue("id", "abc")
	h.PosTicket(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("bad id: got %d, want %d", w.Code, http.StatusNotFound)
	}
}
