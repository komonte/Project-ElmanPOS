package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/customers"
)

// Cobrar fiado y deudores (0027).
func TestRenderCollect(t *testing.T) {
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
	maria := &model.Customer{ID: 5, Name: "María"}
	cashier := canMap(cashierUser)

	t.Run("search", func(t *testing.T) {
		html := render(t, "pos_collect.gohtml", cashier, map[string]any{})
		mustHave(t, html, `hx-get="/pos/collect/customers"`, "¿Quién paga?")
		opts := render(t, "pos_customers.gohtml", cashier, map[string]any{"Collect": true,
			"Customers": []customers.WithDebt{{Customer: maria, Debt: dec("2500")}}})
		mustHave(t, opts, `href="/pos/collect?customer=5"`, "Ya debe $ 2.500,00")
		if strings.Contains(opts, "posPickCustomer") {
			t.Error("collect options must be links")
		}
	})
	t.Run("form", func(t *testing.T) {
		html := render(t, "pos_collect.gohtml", cashier, map[string]any{"Customer": maria, "Debt": dec("2500"),
			"Amount": "2500,00", "Methods": model.PaymentMethods, "Method": model.PaymentCash})
		mustHave(t, html, `action="/pos/collect"`, `name="customer_id" value="5"`, `value="2500,00"`,
			"Debe $ 2.500,00", `<option value="CASH" selected>`)
		if strings.Contains(html, `value="ACCOUNT"`) {
			t.Error("a debt is not paid on account")
		}
		none := render(t, "pos_collect.gohtml", cashier, map[string]any{"Customer": maria, "Debt": dec("0")})
		if strings.Contains(none, `action="/pos/collect"`) {
			t.Error("no form when nothing is owed")
		}
	})
	t.Run("done", func(t *testing.T) {
		html := render(t, "pos_collect.gohtml", cashier, map[string]any{"Done": &customers.Collected{
			Payment: &model.CustomerPayment{Amount: dec("2500")}, Customer: maria, Change: dec("500"), Debt: dec("0")}})
		mustHave(t, html, `id="collect-done"`, "Cobrado $ 2.500,00 de María.", "$ 500,00", "Ya no debe nada.")
	})
	t.Run("debtors and account", func(t *testing.T) {
		paid := "2026-11-20T15:00:00Z"
		html := render(t, "report_debtors.gohtml", adminCan, map[string]any{"Section": "reports_debtors",
			"Total": dec("3500"), "Debtors": []model.Debtor{{CustomerID: 5, Name: "María", Debt: dec("3500"),
				IsActive: true, LastPayment: &paid}}})
		mustHave(t, html, `href="/reports/debtors" aria-current="page"`, "$ 3.500,00", `href="/customers/5/account"`)
		acc := render(t, "customer_account.gohtml", adminCan, map[string]any{"Customer": maria, "Debt": dec("2000"),
			"Entries": []model.AccountEntry{
				{At: paid, SaleNumber: 12, Method: model.PaymentAccount, Amount: dec("3000"), Balance: dec("3000")},
				{At: paid, Method: model.PaymentCash, Amount: dec("-1000"), Balance: dec("2000")}}})
		mustHave(t, acc, "Fiado en la venta N.º 12", "Pagó (Efectivo)", "-$ 1.000,00", "Debe $ 2.000,00")
		if html := render(t, "pos_collect.gohtml", cashier, map[string]any{}); strings.Contains(html, `href="/reports/debtors"`) {
			t.Error("the cashier sees the debtors link")
		}
	})
}
