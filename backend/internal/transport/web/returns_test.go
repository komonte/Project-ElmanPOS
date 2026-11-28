package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sales"
)

// Devoluciones (0028): la pantalla y el menú.
func TestRenderReturns(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, can map[string]bool, data map[string]any) string {
		t.Helper()
		data["Can"] = can
		data["Methods"] = model.SaleMethods
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", "returns.gohtml", data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	sale := &model.Sale{ID: 9, SaleNumber: 12, Status: model.SaleCompleted, CreatedAt: "2026-11-27T15:00:00Z",
		Subtotal: dec("3000"), Total: dec("3000"), CustomerName: "María",
		Items: []*model.SaleItem{
			{ID: 31, ProductName: "SODA", Quantity: dec("3"), UnitPrice: dec("1000")},
			{ID: 32, ProductName: "QUESO", Quantity: dec("0.5"), UnitPrice: dec("8000"), SaleType: model.SaleTypeWeight},
		}}
	view := &sales.ReturnView{Sale: sale, Returned: map[int64]decimal.Decimal{32: dec("0.5")},
		Refunded: dec("4000"), Left: dec("3000")}
	html := render(t, adminCan, map[string]any{"Section": "returns", "View": view, "Number": "12"})
	for _, s := range []string{`href="/returns" aria-current="page"`, "Venta N.º 12", "María", `name="qty_31"`,
		`name="restock_31" checked`, "Ya se devolvieron $ 4.000,00", `<option value="ACCOUNT">Cuenta corriente</option>`,
		`name="reason"`, "function returnRecalc"} {
		if !strings.Contains(html, s) {
			t.Errorf("missing %q", s)
		}
	}
	if strings.Contains(html, `name="qty_32"`) {
		t.Error("a fully returned line still has an input")
	}
	done := render(t, adminCan, map[string]any{"Done": "Devolución de la venta N.º 12 hecha. Devolvé $ 1.000,00 en efectivo."})
	if !strings.Contains(done, `id="return-done"`) {
		t.Error("missing the done notice")
	}
	if strings.Contains(render(t, canMap(cashierUser), map[string]any{}), `href="/returns"`) {
		t.Error("the cashier sees the returns link")
	}
}

func TestRefundList(t *testing.T) {
	got := refundList("es", []*model.Payment{{Method: model.PaymentTransfer, Amount: dec("2000")},
		{Method: model.PaymentCash, Amount: dec("500")}})
	if got != "$ 500,00 en efectivo y $ 2.000,00 por transferencia" {
		t.Errorf("got %q", got)
	}
}
