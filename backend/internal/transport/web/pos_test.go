package web

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sales"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sessions"
)

func postForm(form url.Values) *http.Request {
	r := httptest.NewRequest("POST", "/pos/checkout", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestSaleFromForm(t *testing.T) {
	sale, err := saleFromForm(postForm(url.Values{
		"product_id": {"7", "9"}, "quantity": {"2", "0,350"},
		"pay_method": {"TRANSFER", "CASH"}, "pay_amount": {"1.500,50", "10.000"}, "pay_ref": {"op 1", ""},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(sale.Items) != 2 || sale.Items[0].ProductID != 7 || !sale.Items[1].Quantity.Equal(decimal.RequireFromString("0.35")) {
		t.Errorf("items: got %+v %+v", sale.Items[0], sale.Items[1])
	}
	if len(sale.Payments) != 2 || sale.Payments[0].Method != model.PaymentTransfer ||
		!sale.Payments[0].Amount.Equal(decimal.RequireFromString("1500.5")) || sale.Payments[0].Reference != "op 1" ||
		!sale.Payments[1].Amount.Equal(decimal.RequireFromString("10000")) {
		t.Errorf("payments: got %+v %+v", sale.Payments[0], sale.Payments[1])
	}

	tests := []struct {
		name    string
		form    url.Values
		wantErr error
	}{
		{"bad product", url.Values{"product_id": {"x"}, "quantity": {"1"}}, model.ErrSaleItemProduct},
		{"empty quantity", url.Values{"product_id": {"7"}, "quantity": {""}}, model.ErrInvalidSaleQuantity},
		{"bad quantity", url.Values{"product_id": {"7"}, "quantity": {"dos"}}, model.ErrInvalidSaleQuantity},
		{"lines out of step", url.Values{"product_id": {"7", "8"}, "quantity": {"1"}}, model.ErrInvalidSaleQuantity},
		{"bad amount", url.Values{"product_id": {"7"}, "quantity": {"1"}, "pay_method": {"CASH"},
			"pay_amount": {"mil"}, "pay_ref": {""}}, model.ErrInvalidPaymentAmount},
		{"payments out of step", url.Values{"product_id": {"7"}, "quantity": {"1"}, "pay_method": {"CASH"},
			"pay_amount": {"1"}}, model.ErrInvalidPaymentAmount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := saleFromForm(postForm(tt.form)); !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestPosErrMsg(t *testing.T) {
	h := &WebHandler{}
	r := httptest.NewRequest("POST", "/pos/checkout", nil)
	tests := []struct {
		err  error
		want string
	}{
		{&sales.LineError{ProductName: "SODA", Available: decimal.RequireFromString("2.5"), Err: sales.ErrNotEnoughStock},
			"SODA: no hay stock suficiente (hay 2,5)."},
		{&sales.LineError{ProductName: "YERBA", Err: sales.ErrNoPrice}, "YERBA: no tiene precio. Cargalo en Productos."},
		{&sales.LineError{ProductName: "SODA", Err: fmt.Errorf("%w: %w", service.ErrValidation, model.ErrSaleQuantityNotWhole)},
			"SODA: se vende por unidad: la cantidad tiene que ser entera."},
		{&sales.LineError{ProductID: 99, Err: sales.ErrUnknownProduct}, "El producto no existe."},
		{sales.ErrRegisterClosed, "La caja está cerrada: abrila para cobrar."},
		{fmt.Errorf("%w: %w", service.ErrValidation, model.ErrPaymentsOverTotal), "Solo el efectivo puede pasarse del total"},
	}
	for _, tt := range tests {
		if got := h.posErrText(r, "es", tt.err); !strings.HasPrefix(got, tt.want) {
			t.Errorf("%v: got %q, want %q", tt.err, got, tt.want)
		}
	}
}

func TestRenderPos(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, name string, data map[string]any) string {
		t.Helper()
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", name, data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	page := func(st *sessions.State) map[string]any {
		return map[string]any{"Section": "pos", "TitleKey": "pos.title", "Methods": model.PaymentMethods, "State": st}
	}
	reg := &model.CashRegister{ID: 1, Name: "Caja 1"}

	t.Run("open", func(t *testing.T) {
		html := render(t, "pos.gohtml", page(&sessions.State{Register: reg, Session: &model.CashierSession{ID: 3}}))
		for _, s := range []string{
			// "Vender" destacado en el menú, y la página es la de vender.
			`<a href="/pos" aria-current="page"`, "bg-tag text-ink font-display", `<h1 class="shelf-title text-5xl">Vender</h1>`,
			"Caja 1 abierta",
			// Buscador listo para escanear.
			`id="pos-scan"`, `autofocus`, `hx-get="/pos/search"`, `onkeydown="posScanKey(event, this)"`,
			`id="pos-options"`,
			// Carrito y cobro, conectados al formulario.
			`<form id="pos-form" hx-post="/pos/checkout" hx-target="#pos-result"`, `id="pos-lines"`, `id="pos-total"`,
			`name="product_id"`, `name="quantity"`, `name="pay_method" form="pos-form"`,
			`name="pay_amount" form="pos-form"`, `name="pay_ref" form="pos-form"`,
			`type="submit" form="pos-form" id="pos-charge" disabled`,
			`data-method="CASH"`, ">Efectivo<", ">Débito<", ">Crédito<", ">Transferencia<", ">QR<",
			`onclick="posAddPayment(this.dataset.method)"`,
			// Comportamiento (se prueba en el navegador): que esté conectado.
			"function posAdd(button)", "function posTotals()", "function posParseMoney(s)",
			"addEventListener('beforeunload'", "querySelector('[data-sale-done]')", "htmx:configRequest",
		} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if strings.Contains(html, `id="pos-closed"`) {
			t.Error("open register shows the closed notice")
		}
	})

	t.Run("closed", func(t *testing.T) {
		html := render(t, "pos.gohtml", page(&sessions.State{Register: reg}))
		if !strings.Contains(html, `id="pos-closed"`) || !strings.Contains(html, `href="/cash"`) ||
			strings.Contains(html, `id="pos-form"`) {
			t.Errorf("closed register: got %s", html)
		}
		if html := render(t, "pos.gohtml", page(&sessions.State{})); !strings.Contains(html, `id="pos-closed"`) {
			t.Error("no register must show the closed notice")
		}
	})

	t.Run("options", func(t *testing.T) {
		price, kg := decimal.RequireFromString("1500"), decimal.RequireFromString("5000")
		html := render(t, "pos_options.gohtml", map[string]any{"Query": "s", "Options": []sales.Option{
			{ID: 1, Name: "SODA", SKU: "S1", SaleType: model.SaleTypeUnit, Price: &price, OnHand: decimal.RequireFromString("12")},
			{ID: 2, Name: "QUESO", SKU: "Q1", SaleType: model.SaleTypeWeight, Price: &kg, OnHand: decimal.RequireFromString("2.5")},
			{ID: 3, Name: "YERBA", SKU: "Y1", SaleType: model.SaleTypeUnit, OnHand: decimal.RequireFromString("4")},
		}})
		for _, s := range []string{
			`data-id="1" data-name="SODA" data-type="UNIT"`, `data-price="1500" data-stock="12"`, `onclick="posAdd(this)"`,
			"$ 1.500,00", "stock 12",
			`data-type="WEIGHT"`, "$ 5.000,00 /kg", "stock 2,5 kg",
			`data-id="3"`, `data-price=""`, `disabled aria-disabled="true"`, "sin precio",
		} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		none := render(t, "pos_options.gohtml", map[string]any{"Query": "779123"})
		if !strings.Contains(none, "data-not-found") || !strings.Contains(none, "No se encontró «779123».") {
			t.Errorf("not found: got %s", none)
		}
		if empty := strings.TrimSpace(render(t, "pos_options.gohtml", map[string]any{"Query": ""})); empty != "" {
			t.Errorf("empty query must render nothing: %q", empty)
		}
	})

	t.Run("result", func(t *testing.T) {
		done := render(t, "pos_result.gohtml", map[string]any{"Sale": &model.Sale{SaleNumber: 12,
			Total: decimal.RequireFromString("8500"), Change: decimal.RequireFromString("1500")}})
		for _, s := range []string{"data-sale-done", "Venta N.º 12 cobrada", "$ 8.500,00", "Vuelto", "$ 1.500,00"} {
			if !strings.Contains(done, s) {
				t.Errorf("missing %q", s)
			}
		}
		exact := render(t, "pos_result.gohtml", map[string]any{"Sale": &model.Sale{SaleNumber: 1, Total: decimal.NewFromInt(10)}})
		if strings.Contains(exact, "Vuelto") {
			t.Error("no change, no change line")
		}
		failed := render(t, "pos_result.gohtml", map[string]any{"Error": "SODA: no tiene precio."})
		if !strings.Contains(failed, `role="alert"`) || strings.Contains(failed, "data-sale-done") {
			t.Errorf("error: got %s", failed)
		}
	})
}
