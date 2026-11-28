package sales

import (
	"errors"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Fiado (0026): una parte en efectivo (redondeada) y el resto a cuenta del
// cliente. La deuda sale de las ventas sin anular y el cierre no la cuenta.
func TestCheckoutOnAccount(t *testing.T) {
	f := newFixture(t)
	f.exec(t, `UPDATE app_settings SET value = '100'::jsonb WHERE key = 'cash_rounding_multiple';`)
	var maria, inactive int64
	f.scan(t, &maria, `INSERT INTO customers (name) VALUES ('MARIA TEST SALES') RETURNING id;`)
	f.scan(t, &inactive, `INSERT INTO customers (name, is_active) VALUES ('INACTIVA TEST SALES', FALSE) RETURNING id;`)
	odd := f.newProduct(t, "IMPAR FIADO TEST SALES", `'UNIT', 'UNIT', FALSE, TRUE`, "1234.50", "500", "10")

	// 3 × 1234,50 = 3703,50: 3000 a cuenta, el efectivo debía 703,50 y paga 700.
	sale, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		CustomerID: &maria,
		Items:      []*model.SaleItem{line(odd, "3")},
		Payments:   []*model.Payment{pay(model.PaymentAccount, "3000"), pay(model.PaymentCash, "1000")},
	})
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if sale.CustomerName != "MARIA TEST SALES" || !sale.Total.Equal(d("3700")) || !sale.Change.Equal(d("300")) {
		t.Errorf("customer %q total %s change %s", sale.CustomerName, sale.Total, sale.Change)
	}
	var saved int64
	f.scan(t, &saved, `SELECT customer_id FROM sales WHERE id = $1;`, sale.ID)
	if saved != maria {
		t.Errorf("saved customer %d, want %d", saved, maria)
	}
	debt := func() string {
		debts, err := postgres.NewCustomerStore(f.tx).Debts(f.ctx, []int64{maria})
		if err != nil {
			t.Fatal(err)
		}
		return debts[maria].String()
	}
	if got := debt(); !d(got).Equal(d("3000")) {
		t.Errorf("debt %s, want 3000", got)
	}

	// El arqueo no pide la cuenta corriente y el efectivo esperado es solo
	// el efectivo.
	paid, err := postgres.NewSaleStore(f.tx).PaidByMethod(f.ctx, f.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := model.NewCloseSummary(d("0"), paid, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range audit.Lines {
		if l.Method == model.PaymentAccount {
			t.Error("the close lists ACCOUNT")
		}
	}
	if cash := audit.Line(model.PaymentCash); !cash.Expected.Equal(d("700")) {
		t.Errorf("expected cash %s, want 700", cash.Expected)
	}

	// Anular baja la deuda sola.
	if _, err := f.svc.Void(f.ctx, f.userID, sale.ID, "prueba"); err != nil {
		t.Fatal(err)
	}
	if got := debt(); !d(got).IsZero() {
		t.Errorf("after void, debt %s, want 0", got)
	}

	// Un cliente desactivado o que no existe no se fía.
	for _, id := range []int64{inactive, 999999999} {
		_, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
			CustomerID: &id,
			Items:      []*model.SaleItem{line(odd, "1")},
			Payments:   []*model.Payment{pay(model.PaymentAccount, "1234.50")},
		})
		if !errors.Is(err, ErrCustomerUnavailable) || !errors.Is(err, service.ErrConflict) {
			t.Errorf("customer %d: err %v", id, err)
		}
	}
	// Sin cliente, no.
	_, err = f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		Items:    []*model.SaleItem{line(odd, "1")},
		Payments: []*model.Payment{pay(model.PaymentAccount, "1234.50")},
	})
	if !errors.Is(err, model.ErrSaleNeedsCustomer) || !errors.Is(err, service.ErrValidation) {
		t.Errorf("no customer: err %v", err)
	}
}
