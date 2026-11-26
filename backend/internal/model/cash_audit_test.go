package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

func TestCashAuditValidate(t *testing.T) {
	tests := []struct {
		name    string
		a       *model.CashAudit
		wantErr error
	}{
		{"nil", nil, model.ErrNilCashAudit},
		{"valid", &model.CashAudit{Method: model.PaymentCash, DeclaredAmount: dec("15000.50")}, nil},
		{"zero is fine", &model.CashAudit{Method: model.PaymentQR, DeclaredAmount: dec("0")}, nil},
		{"unknown method", &model.CashAudit{Method: "CHEQUE", DeclaredAmount: dec("1")}, model.ErrInvalidPaymentMethod},
		{"negative", &model.CashAudit{Method: model.PaymentCash, DeclaredAmount: dec("-1")}, model.ErrInvalidDeclaredAmount},
		{"three decimals", &model.CashAudit{Method: model.PaymentCash, DeclaredAmount: dec("1.005")}, model.ErrInvalidDeclaredAmount},
		{"too large", &model.CashAudit{Method: model.PaymentCash, DeclaredAmount: dec("10000000000")}, model.ErrInvalidDeclaredAmount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.a.Validate(); !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewCloseSummary(t *testing.T) {
	// Sesión con $ 5.000 de cambio; ventas cobradas: $ 12.450 en efectivo,
	// $ 3.000 con débito y $ 800 por QR.
	paid := map[string]decimal.Decimal{
		model.PaymentCash:  dec("12450"),
		model.PaymentDebit: dec("3000"),
		model.PaymentQR:    dec("800"),
	}
	declared := []*model.CashAudit{
		{Method: model.PaymentCash, DeclaredAmount: dec("17400")}, // faltan 50
		{Method: model.PaymentDebit, DeclaredAmount: dec("3000")},
		{Method: model.PaymentTransfer, DeclaredAmount: dec("100")}, // sobra: no hubo transferencias
		// QR no se declaró: cuenta como 0 y faltan 800.
	}
	s, err := model.NewCloseSummary(dec("5000"), paid, declared)
	if err != nil {
		t.Fatal(err)
	}

	if len(s.Lines) != len(model.PaymentMethods) {
		t.Fatalf("one line per method: got %d", len(s.Lines))
	}
	for i, m := range model.PaymentMethods {
		if s.Lines[i].Method != m {
			t.Errorf("line %d: got %s, want %s (PaymentMethods order)", i, s.Lines[i].Method, m)
		}
	}
	want := map[string][3]string{ // esperado, declarado, diferencia
		model.PaymentCash:     {"17450", "17400", "-50"},
		model.PaymentDebit:    {"3000", "3000", "0"},
		model.PaymentCredit:   {"0", "0", "0"},
		model.PaymentTransfer: {"0", "100", "100"},
		model.PaymentQR:       {"800", "0", "-800"},
	}
	for m, w := range want {
		l := s.Line(m)
		if !l.Expected.Equal(dec(w[0])) || !l.Declared.Equal(dec(w[1])) || !l.Difference().Equal(dec(w[2])) {
			t.Errorf("%s: got expected %s, declared %s, difference %s; want %v",
				m, l.Expected, l.Declared, l.Difference(), w)
		}
	}
	if !s.HasDifference() {
		t.Error("cash and QR do not match: HasDifference must be true")
	}

	audits := s.Audits(9)
	if len(audits) != len(model.PaymentMethods) {
		t.Fatalf("one audit per method: got %d", len(audits))
	}
	for _, a := range audits {
		if a.CashierSessionID != 9 || !a.DeclaredAmount.Equal(s.Line(a.Method).Declared) {
			t.Errorf("audit %s: got %+v", a.Method, a)
		}
	}
}

func TestNewCloseSummaryExact(t *testing.T) {
	// Sin ventas, cerrar con el cambio exacto no tiene diferencias.
	s, err := model.NewCloseSummary(dec("5000"), nil,
		[]*model.CashAudit{{Method: model.PaymentCash, DeclaredAmount: dec("5000")}})
	if err != nil {
		t.Fatal(err)
	}
	if s.HasDifference() {
		t.Errorf("exact close: got %+v", s.Lines)
	}
	if l := s.Line(model.PaymentCash); !l.Expected.Equal(dec("5000")) {
		t.Errorf("cash expected is the float: got %s", l.Expected)
	}
}

func TestNewCloseSummaryErrors(t *testing.T) {
	tests := []struct {
		name     string
		declared []*model.CashAudit
		wantErr  error
	}{
		{"negative", []*model.CashAudit{{Method: model.PaymentCash, DeclaredAmount: dec("-1")}}, model.ErrInvalidDeclaredAmount},
		{"unknown method", []*model.CashAudit{{Method: "CHEQUE"}}, model.ErrInvalidPaymentMethod},
		{"nil", []*model.CashAudit{nil}, model.ErrNilCashAudit},
		{"twice", []*model.CashAudit{
			{Method: model.PaymentCash, DeclaredAmount: dec("1")},
			{Method: model.PaymentCash, DeclaredAmount: dec("2")},
		}, model.ErrCashAuditDuplicateMethod},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := model.NewCloseSummary(dec("0"), nil, tt.declared); !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateVoidReason(t *testing.T) {
	got, err := model.ValidateVoidReason("  cobré de más  ")
	if err != nil || got != "cobré de más" {
		t.Errorf("trimmed: got %q, %v", got, err)
	}
	if _, err := model.ValidateVoidReason("   "); !errors.Is(err, model.ErrVoidReasonRequired) {
		t.Errorf("blank: got %v", err)
	}
	// 300 caracteres (no bytes) entran; 301 no.
	if _, err := model.ValidateVoidReason(strings.Repeat("ñ", 300)); err != nil {
		t.Errorf("300 chars: got %v", err)
	}
	if _, err := model.ValidateVoidReason(strings.Repeat("ñ", 301)); !errors.Is(err, model.ErrVoidReasonTooLong) {
		t.Errorf("301 chars: got %v", err)
	}
}

func TestSaleCanVoid(t *testing.T) {
	if err := (&model.Sale{Status: model.SaleCompleted}).CanVoid(); err != nil {
		t.Errorf("completed: got %v", err)
	}
	if err := (&model.Sale{Status: model.SaleVoided}).CanVoid(); !errors.Is(err, model.ErrSaleAlreadyVoided) {
		t.Errorf("voided: got %v", err)
	}
	var nilSale *model.Sale
	if err := nilSale.CanVoid(); !errors.Is(err, model.ErrNilSale) {
		t.Errorf("nil: got %v", err)
	}
}
