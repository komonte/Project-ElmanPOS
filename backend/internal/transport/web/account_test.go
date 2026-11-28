package web

import (
	"bytes"
	"net/url"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/customers"
)

// Fiado (0026): el botón y el buscador de clientes en la caja, el ticket y
// el aviso de anulación.
func TestRenderOnAccount(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, name string, data map[string]any) string {
		t.Helper()
		data["Can"] = adminCan
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

	t.Run("pos", func(t *testing.T) {
		html := render(t, "pos.gohtml", map[string]any{"Methods": model.SaleMethods, "State": openState()})
		mustHave(t, html, `data-method="ACCOUNT"`, "Cuenta corriente", `id="pos-customer-box"`,
			`name="customer_id" id="pos-customer-id" form="pos-form"`, `hx-get="/pos/customers"`, "function posPickCustomer")
	})
	t.Run("customer options", func(t *testing.T) {
		c := &model.Customer{ID: 5, Name: "María", Phone: "11 5555"}
		html := render(t, "pos_customers.gohtml", map[string]any{"Query": "mar", "OfferNew": true,
			"Customers": []customers.WithDebt{{Customer: c, Debt: dec("2500")}}})
		mustHave(t, html, `data-customer="5"`, `data-name="María"`, `data-debt="$ 2.500,00"`, "Ya debe $ 2.500,00",
			`data-new-customer data-q="mar"`, "Nuevo cliente «mar»")
		created := render(t, "pos_customers.gohtml", map[string]any{"Created": true,
			"Customers": []customers.WithDebt{{Customer: c}}})
		mustHave(t, created, "data-created")
		if strings.Contains(created, "data-debt") {
			t.Error("a customer who owes nothing has data-debt")
		}
	})
	t.Run("ticket names the customer", func(t *testing.T) {
		s := ticketSale()
		s.CustomerName = "María"
		s.Payments = append(s.Payments, &model.Payment{Method: model.PaymentAccount, Amount: dec("500")})
		html := render(t, "ticket.gohtml", map[string]any{"Lang": "es", "Sale": s})
		mustHave(t, html, "data-customer", "María", "Cuenta corriente")
	})
}

func TestSaleFromFormCustomer(t *testing.T) {
	sale, err := saleFromForm(postForm(url.Values{"product_id": {"7"}, "quantity": {"1"}, "customer_id": {"5"},
		"pay_method": {"ACCOUNT"}, "pay_amount": {"1000"}, "pay_ref": {""}}))
	if err != nil || sale.CustomerID == nil || *sale.CustomerID != 5 {
		t.Fatalf("err %v, customer %v", err, sale.CustomerID)
	}
	none, err := saleFromForm(postForm(url.Values{"product_id": {"7"}, "quantity": {"1"}, "customer_id": {""},
		"pay_method": {"CASH"}, "pay_amount": {"1000"}, "pay_ref": {""}}))
	if err != nil || none.CustomerID != nil {
		t.Errorf("empty customer: err %v, customer %v", err, none.CustomerID)
	}
}

func TestRefundNoticeAccount(t *testing.T) {
	s := &model.Sale{SaleNumber: 3, Payments: []*model.Payment{
		{Method: model.PaymentCash, Amount: dec("700")}, {Method: model.PaymentAccount, Amount: dec("3000")}}}
	got := refundNotice("es", s)
	if !strings.Contains(got, "$ 700,00 en efectivo") || !strings.Contains(got, "$ 3.000,00 menos de deuda en la cuenta corriente") {
		t.Errorf("got %q", got)
	}
}
