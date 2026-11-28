package web

import (
	"bytes"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sales"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sessions"
)

// Artículo sin registrar (0022): el formulario de la caja, las opciones,
// el producto, el reporte y el panel.

func TestSaleFromFormOpenPrice(t *testing.T) {
	sale, err := saleFromForm(postForm(url.Values{
		"product_id": {"7", "9"}, "quantity": {"1", "2"},
		"unit_price": {"", "1.250,50"}, "scanned_code": {"", "7790000000017"},
		"pay_method": {"CASH"}, "pay_amount": {"5000"}, "pay_ref": {""},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !sale.Items[0].UnitPrice.IsZero() || sale.Items[0].ScannedCode != "" {
		t.Errorf("line 1: %+v", sale.Items[0])
	}
	if !sale.Items[1].UnitPrice.Equal(decimal.RequireFromString("1250.5")) || sale.Items[1].ScannedCode != "7790000000017" {
		t.Errorf("line 2: %+v", sale.Items[1])
	}
	_, err = saleFromForm(postForm(url.Values{"product_id": {"9"}, "quantity": {"1"}, "unit_price": {"mucho"},
		"pay_method": {"CASH"}, "pay_amount": {"1"}, "pay_ref": {""}}))
	if !errors.Is(err, sales.ErrOpenPriceMissing) {
		t.Errorf("bad price: got %v", err)
	}
	_, err = saleFromForm(postForm(url.Values{"product_id": {"7", "9"}, "quantity": {"1", "1"}, "unit_price": {""},
		"pay_method": {"CASH"}, "pay_amount": {"1"}, "pay_ref": {""}}))
	if !errors.Is(err, model.ErrInvalidSaleQuantity) {
		t.Errorf("prices out of step: got %v", err)
	}
}

func TestRenderOpenPrice(t *testing.T) {
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
	varios := sales.Option{ID: 40, Name: "VARIOS 21", SKU: "000040", SaleType: model.SaleTypeUnit, OpenPrice: true}

	t.Run("pos: not found offers the open items", func(t *testing.T) {
		html := render(t, "pos_options.gohtml", adminCan, map[string]any{"Query": "7790000000017",
			"Options": []sales.Option{}, "OpenItems": []sales.Option{varios}})
		mustHave(t, html, "data-not-found", `data-open="1"`, `data-code="7790000000017"`, "data-open-offer",
			"Cobrarlo como «VARIOS 21»")
	})
	t.Run("pos: searching an open item by name", func(t *testing.T) {
		html := render(t, "pos_options.gohtml", adminCan, map[string]any{"Query": "varios", "Options": []sales.Option{varios}})
		mustHave(t, html, `data-open="1" data-code=""`, "Precio a mano", `onclick="posAdd(this)"`)
		if strings.Contains(html, "disabled") {
			t.Error("an open item without a price must not be disabled")
		}
	})
	t.Run("pos page has the price and code inputs", func(t *testing.T) {
		html := render(t, "pos.gohtml", adminCan, map[string]any{"Methods": model.PaymentMethods,
			"State": openState()})
		mustHave(t, html, `name="unit_price"`, `name="scanned_code"`, "function posAddOpen", `data-open-price="Escribí el precio."`)
	})

	t.Run("product: the admin can mark it", func(t *testing.T) {
		html := render(t, "product_prices.gohtml", adminCan, map[string]any{"ProductID": 40, "OpenPriceAllowed": true})
		mustHave(t, html, `id="product-open-price"`, `hx-post="/products/40/open-price"`, `name="open_price" value="on"`,
			"Usar como artículo sin registrar")
		on := render(t, "product_prices.gohtml", adminCan, map[string]any{"ProductID": 40, "OpenPrice": true})
		mustHave(t, on, "Precio libre: sí", "Dejar de usarlo")
		if strings.Contains(on, `name="open_price"`) {
			t.Error("turning off must not send open_price=on")
		}
		clerk := render(t, "product_prices.gohtml", stockCan, map[string]any{"ProductID": 40, "OpenPriceAllowed": true})
		if strings.Contains(clerk, `id="product-open-price"`) {
			t.Error("the stock clerk must not see the toggle")
		}
		weight := render(t, "product_prices.gohtml", adminCan, map[string]any{"ProductID": 41})
		if strings.Contains(weight, `id="product-open-price"`) {
			t.Error("a product that cannot be open price must not show the toggle")
		}
	})

	t.Run("report", func(t *testing.T) {
		html := render(t, "report_unregistered.gohtml", stockCan, map[string]any{"Section": "reports_unregistered",
			"TitleKey": "report.unregistered_title", "Codes": []model.UnregisteredCode{{Code: "7790000000017", Sales: 2,
				Quantity: decimal.RequireFromString("3"), LastPrice: decimal.RequireFromString("350"),
				LastSold: "2026-11-28T15:00:00Z", SoldAs: "VARIOS 21"}}})
		mustHave(t, html, "<title>ElmanPOS - Códigos sin registrar</title>", `href="/reports/unregistered" aria-current="page"`,
			`data-code="7790000000017"`, "$ 350,00", "VARIOS 21", `href="/products?search=7790000000017"`)
		empty := render(t, "report_unregistered.gohtml", stockCan, map[string]any{})
		mustHave(t, empty, `id="report-empty"`)
	})

	t.Run("dashboard", func(t *testing.T) {
		html := render(t, "index.gohtml", stockCan, map[string]any{"UnregisteredCount": 2})
		mustHave(t, html, `id="unregistered-banner"`, "2 códigos cobrados sin registrar")
	})
}

// openState es la caja abierta, para dibujar la página de vender.
func openState() *sessions.State {
	return &sessions.State{Register: &model.CashRegister{ID: 1, Name: "Caja 1"}, Session: &model.CashierSession{ID: 3}}
}
