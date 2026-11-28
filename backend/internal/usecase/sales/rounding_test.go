package sales

import (
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

// Redondeo en efectivo (0024): la parte en efectivo baja al múltiplo y la
// diferencia queda en discount_amount.
func TestCheckoutCashRounding(t *testing.T) {
	f := newFixture(t)
	f.exec(t, `UPDATE app_settings SET value = '100'::jsonb WHERE key = 'cash_rounding_multiple';`)
	odd := f.newProduct(t, "IMPAR TEST SALES", `'UNIT', 'UNIT', FALSE, TRUE`, "1234.50", "500", "10")

	// 3 × 1234,50 = 3703,50: con débito 1000, el efectivo debía 2703,50 y
	// paga 2700.
	sale, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		Items:    []*model.SaleItem{line(odd, "3")},
		Payments: []*model.Payment{pay(model.PaymentDebit, "1000"), pay(model.PaymentCash, "5000")},
	})
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if !sale.Subtotal.Equal(d("3703.50")) || !sale.DiscountAmount.Equal(d("3.50")) || !sale.Total.Equal(d("3700")) ||
		!sale.Change.Equal(d("2300")) {
		t.Errorf("subtotal %s discount %s total %s change %s", sale.Subtotal, sale.DiscountAmount, sale.Total, sale.Change)
	}
	var discount, total, cash string
	f.scan(t, &discount, `SELECT discount_amount::text FROM sales WHERE id = $1;`, sale.ID)
	f.scan(t, &total, `SELECT total::text FROM sales WHERE id = $1;`, sale.ID)
	f.scan(t, &cash, `SELECT amount::text FROM payments WHERE sale_id = $1 AND method = 'CASH';`, sale.ID)
	if !d(discount).Equal(d("3.50")) || !d(total).Equal(d("3700")) || !d(cash).Equal(d("2700")) {
		t.Errorf("saved discount %s total %s cash %s", discount, total, cash)
	}

	// Sin efectivo no se redondea.
	card, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		Items:    []*model.SaleItem{line(odd, "1")},
		Payments: []*model.Payment{pay(model.PaymentDebit, "1234.50")},
	})
	if err != nil {
		t.Fatalf("card Checkout: %v", err)
	}
	if !card.DiscountAmount.IsZero() || !card.Total.Equal(d("1234.50")) {
		t.Errorf("card: discount %s total %s", card.DiscountAmount, card.Total)
	}
}
