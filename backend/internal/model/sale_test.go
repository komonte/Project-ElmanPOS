package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

func TestCashierSessionValidate(t *testing.T) {
	tests := []struct {
		name    string
		s       *model.CashierSession
		wantErr error
	}{
		{"nil", nil, model.ErrNilCashierSession},
		{"valid", &model.CashierSession{CashRegisterID: 1, OpeningAmount: dec("5000.50")}, nil},
		{"zero float is fine", &model.CashierSession{CashRegisterID: 1, OpeningAmount: dec("0")}, nil},
		{"no register", &model.CashierSession{OpeningAmount: dec("1")}, model.ErrCashierSessionRegister},
		{"negative", &model.CashierSession{CashRegisterID: 1, OpeningAmount: dec("-1")}, model.ErrInvalidOpeningAmount},
		{"3 decimals", &model.CashierSession{CashRegisterID: 1, OpeningAmount: dec("1.005")}, model.ErrInvalidOpeningAmount},
		{"too large", &model.CashierSession{CashRegisterID: 1, OpeningAmount: dec("10000000000")}, model.ErrInvalidOpeningAmount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.s.Validate(); !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func saleLine(productID int64, qty string) *model.SaleItem {
	return &model.SaleItem{ProductID: productID, Quantity: dec(qty)}
}

func pay(method, amount string) *model.Payment {
	return &model.Payment{Method: method, Amount: dec(amount)}
}

func validSale() *model.Sale {
	return &model.Sale{
		Items:    []*model.SaleItem{saleLine(1, "2"), saleLine(2, "0.350")},
		Payments: []*model.Payment{pay(model.PaymentCash, "1000"), {Method: model.PaymentTransfer, Amount: dec("500"), Reference: "  op 123 "}},
	}
}

func TestSaleValidate(t *testing.T) {
	s := validSale()
	if err := s.Validate(); err != nil {
		t.Fatalf("valid sale: %v", err)
	}
	if got := s.Payments[1].Reference; got != "op 123" {
		t.Errorf("reference not trimmed: %q", got)
	}

	tests := []struct {
		name    string
		change  func(s *model.Sale)
		wantErr error
	}{
		{"no lines", func(s *model.Sale) { s.Items = nil }, model.ErrSaleNoItems},
		{"nil line", func(s *model.Sale) { s.Items[1] = nil }, model.ErrNilSaleItem},
		{"no product", func(s *model.Sale) { s.Items[0].ProductID = 0 }, model.ErrSaleItemProduct},
		{"zero quantity", func(s *model.Sale) { s.Items[0].Quantity = dec("0") }, model.ErrInvalidSaleQuantity},
		{"negative quantity", func(s *model.Sale) { s.Items[0].Quantity = dec("-1") }, model.ErrInvalidSaleQuantity},
		{"4 decimals", func(s *model.Sale) { s.Items[1].Quantity = dec("0.3505") }, model.ErrInvalidSaleQuantity},
		{"same product twice", func(s *model.Sale) { s.Items[1].ProductID = 1 }, model.ErrSaleDuplicateProduct},
		{"no payments", func(s *model.Sale) { s.Payments = nil }, model.ErrSaleNoPayments},
		{"nil payment", func(s *model.Sale) { s.Payments[0] = nil }, model.ErrNilPayment},
		{"unknown method", func(s *model.Sale) { s.Payments[0].Method = "CHEQUE" }, model.ErrInvalidPaymentMethod},
		{"zero amount", func(s *model.Sale) { s.Payments[0].Amount = dec("0") }, model.ErrInvalidPaymentAmount},
		{"3 decimals amount", func(s *model.Sale) { s.Payments[0].Amount = dec("1.001") }, model.ErrInvalidPaymentAmount},
		{"long reference", func(s *model.Sale) { s.Payments[1].Reference = strings.Repeat("x", 121) }, model.ErrPaymentReferenceTooLong},
		{"two cash payments", func(s *model.Sale) { s.Payments[1].Method = model.PaymentCash }, model.ErrSaleTwoCashPayments},
		{"two transfers are fine", func(s *model.Sale) { s.Payments[0].Method = model.PaymentTransfer }, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validSale()
			tt.change(s)
			if err := s.Validate(); !errors.Is(err, tt.wantErr) {
				t.Errorf("Validate() = %v, want %v", err, tt.wantErr)
			}
		})
	}

	var nilSale *model.Sale
	if err := nilSale.Validate(); !errors.Is(err, model.ErrNilSale) {
		t.Errorf("nil sale: %v", err)
	}
}

func TestSaleItemValidateQuantity(t *testing.T) {
	ceiling := dec("50")
	tests := []struct {
		name     string
		saleType model.SaleType
		qty      string
		wantErr  error
	}{
		{"unit whole", model.SaleTypeUnit, "3", nil},
		{"unit fraction", model.SaleTypeUnit, "1.5", model.ErrSaleQuantityNotWhole},
		{"weight grams", model.SaleTypeWeight, "0.300", nil},
		{"weight at the ceiling", model.SaleTypeWeight, "50", nil},
		{"weight over the ceiling", model.SaleTypeWeight, "50.001", model.ErrSaleWeightTooLarge},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := saleLine(1, tt.qty).ValidateQuantity(tt.saleType, ceiling); !errors.Is(err, tt.wantErr) {
				t.Errorf("ValidateQuantity() = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func pricedLine(qty, price, rate string) *model.SaleItem {
	return &model.SaleItem{ProductID: 1, Quantity: dec(qty), UnitPrice: dec(price), TaxRate: dec(rate)}
}

func TestSaleComputeTotals(t *testing.T) {
	s := &model.Sale{Items: []*model.SaleItem{
		pricedLine("2", "1210", "21"),          // 2420.00, IVA 420.00
		pricedLine("0.333", "1000.01", "10.5"), // 333.00333 → 333.00, IVA 31.6425...
		pricedLine("1", "500", "0"),            // exento
	}}
	if err := s.ComputeTotals(); err != nil {
		t.Fatal(err)
	}
	if got := s.Items[1].LineTotal(); !got.Equal(dec("333.00")) {
		t.Errorf("weight line total = %s, want 333.00", got)
	}
	if !s.Subtotal.Equal(dec("3253.00")) || !s.Total.Equal(dec("3253.00")) {
		t.Errorf("subtotal/total = %s/%s, want 3253.00", s.Subtotal, s.Total)
	}
	// 420 + 333 × 10.5 / 110.5 = 420 + 31.6425... = 451.64
	if !s.TaxAmount.Equal(dec("451.64")) {
		t.Errorf("tax = %s, want 451.64", s.TaxAmount)
	}
	if !s.DiscountAmount.IsZero() {
		t.Errorf("discount = %s, want 0", s.DiscountAmount)
	}

	// El IVA se redondea una vez: tres renglones de $1 al 21 % tienen
	// 0.1735... cada uno; redondear por renglón daría 0.51.
	s = &model.Sale{Items: []*model.SaleItem{pricedLine("1", "1", "21"), pricedLine("1", "1", "21"), pricedLine("1", "1", "21")}}
	if err := s.ComputeTotals(); err != nil {
		t.Fatal(err)
	}
	if !s.TaxAmount.Equal(dec("0.52")) {
		t.Errorf("tax rounded per line: %s, want 0.52", s.TaxAmount)
	}

	s = &model.Sale{Items: []*model.SaleItem{pricedLine("0.001", "1", "21")}}
	if err := s.ComputeTotals(); !errors.Is(err, model.ErrSaleLineTotalZero) {
		t.Errorf("line rounding to 0: %v", err)
	}
	s = &model.Sale{Items: []*model.SaleItem{pricedLine("2", "9999999999.99", "21")}}
	if err := s.ComputeTotals(); !errors.Is(err, model.ErrSaleTotalTooLarge) {
		t.Errorf("total too large: %v", err)
	}
}

func TestSaleSettlePayments(t *testing.T) {
	sale := func(payments ...*model.Payment) *model.Sale {
		return &model.Sale{Subtotal: dec("8500"), Total: dec("8500"), Payments: payments}
	}

	t.Run("exact", func(t *testing.T) {
		s := sale(pay(model.PaymentDebit, "8500"))
		if err := s.SettlePayments(decimal.Zero); err != nil || !s.Change.IsZero() {
			t.Errorf("err %v, change %s", err, s.Change)
		}
	})
	t.Run("cash with change", func(t *testing.T) {
		s := sale(pay(model.PaymentCash, "10000"))
		if err := s.SettlePayments(decimal.Zero); err != nil {
			t.Fatal(err)
		}
		c := s.Payments[0]
		if !s.Change.Equal(dec("1500")) || !c.Amount.Equal(dec("8500")) || !c.Tendered.Equal(dec("10000")) {
			t.Errorf("change %s, amount %s, tendered %s", s.Change, c.Amount, c.Tendered)
		}
		if !s.PaymentsTotal().Equal(s.Total) {
			t.Errorf("payments %s != total %s", s.PaymentsTotal(), s.Total)
		}
	})
	t.Run("split with cash change", func(t *testing.T) {
		s := sale(pay(model.PaymentTransfer, "3000"), pay(model.PaymentCash, "6000"))
		if err := s.SettlePayments(decimal.Zero); err != nil {
			t.Fatal(err)
		}
		if !s.Change.Equal(dec("500")) || !s.Payments[1].Amount.Equal(dec("5500")) {
			t.Errorf("change %s, cash %s", s.Change, s.Payments[1].Amount)
		}
	})
	t.Run("exact cash keeps tendered", func(t *testing.T) {
		s := sale(pay(model.PaymentCash, "8500"))
		if err := s.SettlePayments(decimal.Zero); err != nil || !s.Payments[0].Tendered.Equal(dec("8500")) {
			t.Errorf("err %v, tendered %s", err, s.Payments[0].Tendered)
		}
	})
	t.Run("short", func(t *testing.T) {
		s := sale(pay(model.PaymentCash, "8000"))
		if err := s.SettlePayments(decimal.Zero); !errors.Is(err, model.ErrPaymentsShort) {
			t.Errorf("err %v", err)
		}
	})
	t.Run("card over the total", func(t *testing.T) {
		s := sale(pay(model.PaymentCredit, "9000"))
		if err := s.SettlePayments(decimal.Zero); !errors.Is(err, model.ErrPaymentsOverTotal) {
			t.Errorf("err %v", err)
		}
	})
	t.Run("cash not needed", func(t *testing.T) {
		s := sale(pay(model.PaymentDebit, "8500"), pay(model.PaymentCash, "1000"))
		if err := s.SettlePayments(decimal.Zero); !errors.Is(err, model.ErrPaymentsOverTotal) {
			t.Errorf("err %v", err)
		}
	})
}

// Redondeo en efectivo (0024): solo la parte en efectivo baja al múltiplo.
func TestSaleSettlePaymentsCashRounding(t *testing.T) {
	hundred := dec("100")
	sale := func(subtotal string, payments ...*model.Payment) *model.Sale {
		// Un renglón con IVA 21 %: el IVA de adentro se prorratea.
		return &model.Sale{Subtotal: dec(subtotal), Total: dec(subtotal),
			Items: []*model.SaleItem{pricedLine("1", subtotal, "21")}, Payments: payments}
	}
	tests := []struct {
		name                    string
		s                       *model.Sale
		roundTo                 decimal.Decimal
		discount, total, change string
		cash                    string // lo que se aplica en efectivo
	}{
		{"all cash, exact", sale("12345", pay(model.PaymentCash, "12300")), hundred, "45", "12300", "0", "12300"},
		{"all cash, with change", sale("12345", pay(model.PaymentCash, "20000")), hundred, "45", "12300", "7700", "12300"},
		{"tendered the unrounded amount", sale("12345", pay(model.PaymentCash, "12345")), hundred, "45", "12300", "45", "12300"},
		{"card first, cash the rest", sale("12345", pay(model.PaymentDebit, "5000"), pay(model.PaymentCash, "7300")),
			hundred, "45", "12300", "0", "7300"},
		{"card part is never rounded", sale("12345", pay(model.PaymentDebit, "12345")), hundred, "0", "12345", "0", ""},
		{"already a multiple", sale("12300", pay(model.PaymentCash, "12300")), hundred, "0", "12300", "0", "12300"},
		{"cash below the multiple is not rounded", sale("80", pay(model.PaymentCash, "100")), hundred, "0", "80", "20", "80"},
		{"card leaves less than the multiple", sale("12345", pay(model.PaymentDebit, "12300"), pay(model.PaymentCash, "45")),
			hundred, "0", "12345", "0", "45"},
		{"off", sale("12345", pay(model.PaymentCash, "12345")), decimal.Zero, "0", "12345", "0", "12345"},
		{"multiple of 10", sale("12345", pay(model.PaymentCash, "12340")), dec("10"), "5", "12340", "0", "12340"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.s.SettlePayments(tt.roundTo); err != nil {
				t.Fatal(err)
			}
			if !tt.s.DiscountAmount.Equal(dec(tt.discount)) || !tt.s.Total.Equal(dec(tt.total)) ||
				!tt.s.Change.Equal(dec(tt.change)) {
				t.Errorf("discount %s total %s change %s, want %s %s %s",
					tt.s.DiscountAmount, tt.s.Total, tt.s.Change, tt.discount, tt.total, tt.change)
			}
			if !tt.s.Total.Equal(tt.s.Subtotal.Sub(tt.s.DiscountAmount)) || !tt.s.PaymentsTotal().Equal(tt.s.Total) {
				t.Errorf("total %s, subtotal %s, payments %s", tt.s.Total, tt.s.Subtotal, tt.s.PaymentsTotal())
			}
			for _, p := range tt.s.Payments {
				if p.Method == model.PaymentCash && !p.Amount.Equal(dec(tt.cash)) {
					t.Errorf("cash applied %s, want %s", p.Amount, tt.cash)
				}
			}
		})
	}

	// El IVA de adentro baja con el total: 12.300 × 21 / 121 = 2.134,71.
	s := sale("12345", pay(model.PaymentCash, "12300"))
	if err := s.SettlePayments(hundred); err != nil {
		t.Fatal(err)
	}
	if !s.TaxAmount.Equal(dec("2134.71")) {
		t.Errorf("tax %s, want 2134.71", s.TaxAmount)
	}
	// Llamarla dos veces da lo mismo.
	if err := s.SettlePayments(hundred); err != nil || !s.Total.Equal(dec("12300")) {
		t.Errorf("second call: err %v, total %s", err, s.Total)
	}
	// Con el efectivo redondeado, entregar menos que eso no alcanza.
	short := sale("12345", pay(model.PaymentCash, "12299"))
	if err := short.SettlePayments(hundred); !errors.Is(err, model.ErrPaymentsShort) {
		t.Errorf("short: err %v", err)
	}
}

func TestValidateCashRounding(t *testing.T) {
	for _, m := range model.CashRoundingMultiples {
		if err := model.ValidateCashRounding(m); err != nil {
			t.Errorf("%d: %v", m, err)
		}
	}
	for _, m := range []int64{-100, 1, 99, 200, 5000} {
		if err := model.ValidateCashRounding(m); !errors.Is(err, model.ErrInvalidCashRounding) {
			t.Errorf("%d: err %v", m, err)
		}
	}
}
