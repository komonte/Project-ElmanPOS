package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/purchases"
)

// Precios por factura (F-06).

func pricedRow(id int64, name string, state model.PriceState, price, suggested string) purchases.PricedRow {
	d := func(s string) *decimal.Decimal {
		if s == "" {
			return nil
		}
		v := decimal.RequireFromString(s)
		return &v
	}
	row := purchases.PricedRow{PricedProduct: model.PricedProduct{ProductID: id, SKU: "00000" + name[:1], Name: name, Active: true,
		SaleType: model.SaleTypeUnit, Quantity: decimal.RequireFromString("10"), UnitCost: decimal.RequireFromString("100"),
		AvgCost: decimal.RequireFromString("95"), Price: d(price)}, State: state, Suggested: d(suggested)}
	if price != "" {
		now := time.Now()
		row.PriceFrom = &now
	}
	return row
}

func TestProposedPrice(t *testing.T) {
	for _, c := range []struct {
		row  purchases.PricedRow
		lang string
		want string
	}{
		{pricedRow(1, "OREO", model.PriceMissing, "", "1512.5"), "es", "1512,50"}, // sin precio: el sugerido
		{pricedRow(1, "OREO", model.PriceOld, "1400", "1512.5"), "es", "1400,00"}, // con precio: el actual
		{pricedRow(1, "OREO", model.PriceOld, "1400", ""), "en", "1400.00"},
		{pricedRow(1, "OREO", model.PriceMissing, "", ""), "es", ""},
	} {
		if got := proposedPrice(c.lang, c.row); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.row.State, got, c.want)
		}
	}
}

func TestRenderPurchasePrices(t *testing.T) {
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
	ana := "Ana"
	rows := []purchases.PricedRow{
		pricedRow(1, "OREO", model.PriceMissing, "", "1512.5"),
		pricedRow(2, "SODA", model.PriceOld, "1800", "1900"),
		pricedRow(3, "YERBA", model.PriceOK, "2500", ""),
	}
	off := pricedRow(4, "VIEJO", model.PriceMissing, "", "")
	off.Active = false
	rows = append(rows, off)
	table := func(can bool) pricedTable {
		t := pricedTable{Action: "/purchases/9/prices", CanSet: can, Invoice: true}
		for _, r := range rows {
			t.Rows = append(t.Rows, pricedView{PricedRow: r, Value: proposedPrice("es", r)})
			if r.Active && r.State != model.PriceOK {
				t.Pending++
			}
		}
		return t
	}
	inv := &purchases.Invoice{Summary: &model.PurchaseSummary{ID: 9, ReceivedAt: "2026-11-26T15:00:00Z", SupplierName: "Arcor",
		VoucherNumber: "A-0001", ReceivedByName: &ana, Total: decimal.RequireFromString("5000"), Products: 4, Unpriced: 2}}

	t.Run("history", func(t *testing.T) {
		list := []model.PurchaseSummary{*inv.Summary, {ID: 8, ReceivedAt: "2026-11-25T15:00:00Z", SupplierName: "Coca", Products: 1,
			Total: decimal.RequireFromString("100")}}
		html := render(t, "purchase_history.gohtml", stockCan, map[string]any{"Section": "purchase_history",
			"TitleKey": "purchase.history_title", "Purchases": list, "Limit": 100})
		for _, s := range []string{"<title>ElmanPOS - Compras recibidas</title>", `href="/purchases/history" aria-current="page"`,
			`data-filter="all" aria-current="true"`, `href="/purchases/9"`, "Recibió Ana", "Recibió —", "A-0001",
			`data-unpriced="2"`, "2 sin cotizar", `data-unpriced="0"`, "Cotizada"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})

	t.Run("invoice as admin: states and the price form", func(t *testing.T) {
		html := render(t, "purchase_invoice.gohtml", adminCan, map[string]any{"Section": "purchase_history",
			"TitleKey": "purchase.invoice_title", "Invoice": inv, "Table": table(true)})
		for _, s := range []string{"Compra N° 9", "Arcor", "Recibió Ana", `id="invoice-state"`, "2 sin cotizar",
			`hx-post="/purchases/9/prices"`, `data-product="1" data-state="MISSING"`, ">Sin precio</span>",
			">Precio anterior</span>", "✓ Cotizado", `name="price" value="1512,50"`, `name="price" value="1800,00"`,
			`name="only" value="1"`, `id="prices-save-all"`, "Guardar los sin cotizar", "producto desactivado",
			"this.nextElementSibling.click()"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		// El inactivo no tiene campo.
		if strings.Contains(html, `name="only" value="4"`) {
			t.Error("inactive product has a price field")
		}
	})

	t.Run("invoice as stock clerk: only looks", func(t *testing.T) {
		html := render(t, "purchase_invoice.gohtml", stockCan, map[string]any{"Invoice": inv, "Table": table(false)})
		for _, s := range []string{`name="price"`, `id="prices-save-all"`, "Precio de venta</th>"} {
			if strings.Contains(html, s) {
				t.Errorf("unexpected %q", s)
			}
		}
		if !strings.Contains(html, ">Sin precio</span>") {
			t.Error("the state must show")
		}
	})

	t.Run("all priced: no save-all button", func(t *testing.T) {
		tb := table(true)
		tb.Pending = 0
		html := render(t, "priced_table.gohtml", adminCan, map[string]any{"Table": tb})
		if strings.Contains(html, `id="prices-save-all"`) {
			t.Error("nothing pending: no save-all")
		}
		if table(true).Pending != 2 {
			t.Errorf("pending: %d, want 2 (the inactive one does not count)", table(true).Pending)
		}
	})

	t.Run("table after saving and with an error", func(t *testing.T) {
		tb := table(true)
		tb.Saved = 2
		html := render(t, "priced_table.gohtml", adminCan, map[string]any{"Table": tb})
		if !strings.Contains(html, `id="prices-saved"`) || !strings.Contains(html, "Se guardaron 2 precios.") {
			t.Error("saved: missing notice")
		}
		tb = table(true)
		tb.Error = "SODA: El precio tiene que ser mayor a 0."
		tb.Rows[1].Value, tb.Rows[1].Error = "0", true
		html = render(t, "priced_table.gohtml", adminCan, map[string]any{"Table": tb})
		for _, s := range []string{`role="alert"`, "SODA: El precio", `data-product="2" data-state="OLD" class="bg-danger-soft"`, `value="0"`} {
			if !strings.Contains(html, s) {
				t.Errorf("error: missing %q", s)
			}
		}
	})

	t.Run("missing prices", func(t *testing.T) {
		tb := pricedTable{Action: "/prices/missing", CanSet: true, Pending: 1}
		tb.Rows = []pricedView{{PricedRow: pricedRow(1, "OREO", model.PriceMissing, "", ""), Value: ""}}
		html := render(t, "prices_missing.gohtml", adminCan, map[string]any{"Section": "prices_missing",
			"TitleKey": "price.missing_title", "Table": tb})
		for _, s := range []string{"<title>ElmanPOS - Productos sin precio</title>", `hx-post="/prices/missing"`, ">Stock</th>",
			"Último costo", "Guardar todos"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if strings.Contains(html, "Estado</th>") {
			t.Error("no states outside an invoice")
		}
	})

	t.Run("dashboard: pending prices for the admin only", func(t *testing.T) {
		html := render(t, "index.gohtml", adminCan, map[string]any{"Section": "dashboard", "TitleKey": "nav.dashboard",
			"Pending": &purchases.Pending{MissingPrices: 3, UnpricedPurchases: 1}})
		for _, s := range []string{`id="pending-prices"`, `href="/prices/missing"`, "3 productos con stock y sin precio",
			`href="/purchases/history?unpriced=1"`, "1 factura sin cotizar"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		html = render(t, "index.gohtml", adminCan, map[string]any{"Pending": &purchases.Pending{}})
		if strings.Contains(html, `id="pending-prices"`) {
			t.Error("nothing pending: no card")
		}
		html = render(t, "index.gohtml", stockCan, map[string]any{})
		if strings.Contains(html, `id="pending-prices"`) {
			t.Error("stock clerk: no card")
		}
	})

	t.Run("product form: a sale price on create, for the admin", func(t *testing.T) {
		data := map[string]any{"Product": &model.Product{}, "Price": "1.500"}
		html := render(t, "product_form.gohtml", adminCan, data)
		if !strings.Contains(html, `id="product-price" name="price"`) || !strings.Contains(html, `value="1.500"`) {
			t.Error("admin create: missing the price field with what was typed")
		}
		html = render(t, "product_form.gohtml", stockCan, map[string]any{"Product": &model.Product{}})
		if strings.Contains(html, `id="product-price"`) {
			t.Error("stock clerk: no price field")
		}
		html = render(t, "product_form.gohtml", adminCan, map[string]any{"Product": &model.Product{ID: 5}})
		if strings.Contains(html, `id="product-price" name="price"`) {
			t.Error("edit: the price has its own section")
		}
	})
}

// El precio del alta solo se toma de quien puede poner precios.
func TestNewProductRequestPrice(t *testing.T) {
	h := &WebHandler{}
	req := func(u *model.User, price string) *http.Request {
		r := httptest.NewRequest("POST", "/products/new", strings.NewReader("price="+url.QueryEscape(price)))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return r.WithContext(transport.WithUser(r.Context(), u))
	}
	got, key := h.newProductRequest(req(adminUser, "1.500,50"), &model.Product{})
	if key != "" || got.Price == nil || !got.Price.Equal(decimal.RequireFromString("1500.50")) || got.By != adminUser.ID {
		t.Errorf("admin: %+v, %q", got, key)
	}
	if _, key := h.newProductRequest(req(adminUser, "mil"), &model.Product{}); key != "error.price_invalid" {
		t.Errorf("bad price: %q", key)
	}
	if got, _ := h.newProductRequest(req(stockUser, "1500"), &model.Product{}); got.Price != nil {
		t.Error("stock clerk: the price is ignored")
	}
	if got, _ := h.newProductRequest(req(adminUser, " "), &model.Product{}); got.Price != nil {
		t.Error("empty: no price")
	}
}

func TestNewPricedTable(t *testing.T) {
	off := pricedRow(4, "VIEJO", model.PriceMissing, "", "")
	off.Active = false
	rows := []purchases.PricedRow{pricedRow(1, "OREO", model.PriceMissing, "", "10"), pricedRow(2, "SODA", model.PriceOld, "20", ""),
		pricedRow(3, "YERBA", model.PriceOK, "30", ""), off}
	r := httptest.NewRequest("GET", "/purchases/9", nil)
	r = r.WithContext(transport.WithUser(r.Context(), adminUser))
	tb := newPricedTable(r, rows, "/purchases/9/prices", true, map[int64]string{2: "25"}, 2)
	if tb.Pending != 2 || !tb.CanSet || tb.Rows[0].Value != "10.00" || tb.Rows[1].Value != "25" || !tb.Rows[1].Error || tb.Rows[0].Error {
		t.Errorf("table: pending %d, can %v, rows %+v", tb.Pending, tb.CanSet, tb.Rows)
	}
	r = r.WithContext(transport.WithUser(r.Context(), stockUser))
	if newPricedTable(r, rows, "", true, nil, 0).CanSet {
		t.Error("stock clerk cannot set prices")
	}
}
