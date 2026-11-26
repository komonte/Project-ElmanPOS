package model

import (
	"errors"

	"github.com/shopspring/decimal"
)

// Estados de una sesión de caja (ENUM session_status).
const (
	SessionOpen   = "OPEN"
	SessionClosed = "CLOSED"
)

var (
	ErrNilCashierSession         = errors.New("cashier session cannot be nil")
	ErrCashierSessionRegister    = errors.New("cashier session register is required")
	ErrInvalidOpeningAmount      = errors.New("opening amount must be between 0 and 9999999999.99, with up to 2 decimals")
	ErrCashierSessionNotOpen     = errors.New("cashier session is not open")
	ErrCashierSessionAlreadyOpen = errors.New("cash register already has an open session")
)

// CashierSession es un turno de una caja (02 4.15, F-11/F-12): se abre con
// el efectivo inicial (el cambio), acumula ventas y se cierra con el arqueo.
// Una caja tiene a lo sumo una sesión abierta (BR-02, índice de la 0017), y
// sin sesión abierta no se vende.
type CashierSession struct {
	ID             int64 `json:"id" db:"id"`
	CashRegisterID int64 `json:"cash_register_id" db:"cash_register_id"`
	// OpenedBy queda en nil hasta que haya login (F-13).
	OpenedBy      *int64           `json:"opened_by" db:"opened_by"`
	OpeningAmount decimal.Decimal  `json:"opening_amount" db:"opening_amount"`
	ClosingAmount *decimal.Decimal `json:"closing_amount" db:"closing_amount"` // lo declarado al cerrar
	Status        string           `json:"status" db:"status"`
	OpenedAt      string           `json:"opened_at" db:"opened_at"`
	ClosedAt      *string          `json:"closed_at" db:"closed_at"`

	// Solo lectura, para mostrar.
	RegisterName string `json:"register_name" db:"-"`
}

// Validate valida lo que se escribe al abrir. Que la caja exista y no tenga
// otra sesión abierta lo revisa el caso de uso (y la DB).
func (s *CashierSession) Validate() error {
	if s == nil {
		return ErrNilCashierSession
	}
	if s.CashRegisterID <= 0 {
		return ErrCashierSessionRegister
	}
	if !validMoney(s.OpeningAmount) {
		return ErrInvalidOpeningAmount
	}
	return nil
}

// validMoney: NUMERIC(12,2) que no puede ser negativo.
func validMoney(d decimal.Decimal) bool {
	return !d.IsNegative() && !d.GreaterThan(maxAmount) && d.Equal(d.Round(2))
}
