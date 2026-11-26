package model

import (
	"errors"
	"fmt"
	"slices"

	"github.com/shopspring/decimal"
)

var (
	ErrNilCashAudit             = errors.New("declared amount cannot be nil")
	ErrInvalidDeclaredAmount    = errors.New("declared amount must be between 0 and 9999999999.99, with up to 2 decimals")
	ErrCashAuditDuplicateMethod = errors.New("each payment method can be declared only once")
)

// CashAudit es lo que se declara al cerrar la caja para un medio de pago
// (02 4.20, F-12): lo contado en efectivo, o lo que dice el posnet o la app
// para los otros. Uno por medio y sesión (índice de la 0018).
type CashAudit struct {
	ID               int64           `json:"id" db:"id"`
	CashierSessionID int64           `json:"cashier_session_id" db:"cashier_session_id"`
	Method           string          `json:"method" db:"method"`
	DeclaredAmount   decimal.Decimal `json:"declared_amount" db:"declared_amount"`
	CreatedAt        string          `json:"created_at" db:"created_at"`
}

// Validate valida un monto declarado: medio conocido y plata (0 vale).
func (a *CashAudit) Validate() error {
	if a == nil {
		return ErrNilCashAudit
	}
	if !slices.Contains(PaymentMethods, a.Method) {
		return ErrInvalidPaymentMethod
	}
	if !validMoney(a.DeclaredAmount) {
		return ErrInvalidDeclaredAmount
	}
	return nil
}

// CloseLine es el arqueo de un medio: lo que tendría que haber, lo que se
// declaró y la diferencia.
type CloseLine struct {
	Method   string          `json:"method"`
	Expected decimal.Decimal `json:"expected"`
	Declared decimal.Decimal `json:"declared"`
}

// Difference es declarado − esperado: positiva es sobrante, negativa
// faltante.
func (l CloseLine) Difference() decimal.Decimal {
	return l.Declared.Sub(l.Expected)
}

// CloseSummary es el arqueo de una sesión: una línea por medio de pago, en
// el orden de PaymentMethods.
type CloseSummary struct {
	Lines []CloseLine `json:"lines"`
}

// NewCloseSummary arma el arqueo (F-12). Lo esperado sale de la sesión: en
// efectivo, el inicial más los pagos en efectivo; en los otros medios, sus
// pagos. paid son los pagos de las ventas COMPLETED por medio (las anuladas
// no cuentan). declared es lo que se escribió al cerrar: un medio que no
// está cuenta como declarado en 0, y si tenía pagos sale como faltante.
// Valida cada monto y que ningún medio esté dos veces.
func NewCloseSummary(opening decimal.Decimal, paid map[string]decimal.Decimal, declared []*CashAudit) (*CloseSummary, error) {
	got := make(map[string]decimal.Decimal, len(declared))
	for i, a := range declared {
		if err := a.Validate(); err != nil {
			return nil, fmt.Errorf("declared %d: %w", i+1, err)
		}
		if _, dup := got[a.Method]; dup {
			return nil, ErrCashAuditDuplicateMethod
		}
		got[a.Method] = a.DeclaredAmount
	}
	s := &CloseSummary{Lines: make([]CloseLine, 0, len(PaymentMethods))}
	for _, m := range PaymentMethods {
		expected := paid[m] // cero si no hubo pagos
		if m == PaymentCash {
			expected = expected.Add(opening)
		}
		s.Lines = append(s.Lines, CloseLine{Method: m, Expected: expected, Declared: got[m]})
	}
	return s, nil
}

// Line es la línea de un medio (cero si el medio no existe).
func (s *CloseSummary) Line(method string) CloseLine {
	for _, l := range s.Lines {
		if l.Method == method {
			return l
		}
	}
	return CloseLine{Method: method}
}

// HasDifference dice si algún medio no cierra.
func (s *CloseSummary) HasDifference() bool {
	for _, l := range s.Lines {
		if !l.Difference().IsZero() {
			return true
		}
	}
	return false
}

// Audits son las filas de cash_audits que se guardan: una por medio, con lo
// declarado (0 si no se escribió nada).
func (s *CloseSummary) Audits(sessionID int64) []*CashAudit {
	out := make([]*CashAudit, 0, len(s.Lines))
	for _, l := range s.Lines {
		out = append(out, &CashAudit{CashierSessionID: sessionID, Method: l.Method, DeclaredAmount: l.Declared})
	}
	return out
}
