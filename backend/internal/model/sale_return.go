package model

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

// Devoluciones (2026-11-28, migración 0028): parte o toda una venta, de
// cualquier día. Ver usecase/sales/returns.go.

// ReturnReasonMaxLen es el largo de sale_returns.reason.
const ReturnReasonMaxLen = 300

var (
	ErrReturnReasonRequired = errors.New("return reason is required")
	ErrReturnReasonTooLong  = errors.New("return reason cannot exceed 300 characters")
	ErrReturnNoLines        = errors.New("choose at least one line to return")
	ErrReturnQuantity       = errors.New("return quantity must be greater than 0, with up to 3 decimals")
)

// SaleReturn es una devolución.
type SaleReturn struct {
	ID               int64             `json:"id"`
	SaleID           int64             `json:"sale_id"`
	CashierSessionID int64             `json:"cashier_session_id"`
	Reason           string            `json:"reason"`
	Total            decimal.Decimal   `json:"total"`
	CreatedBy        int64             `json:"created_by"`
	CreatedAt        string            `json:"created_at"`
	Items            []*SaleReturnItem `json:"items"`
	Payments         []*Payment        `json:"payments"` // SaleID sin usar
}

// SaleReturnItem es lo devuelto de un renglón. Restock: vuelve a la
// góndola (stock); si no, es merma y no mueve stock. Amount es lo que se
// devuelve por él.
type SaleReturnItem struct {
	ID         int64           `json:"id"`
	SaleItemID int64           `json:"sale_item_id"`
	Quantity   decimal.Decimal `json:"quantity"`
	Amount     decimal.Decimal `json:"amount"`
	Restock    bool            `json:"restock"`

	// Solo lectura, para mostrar.
	ProductName string `json:"product_name,omitempty"`
}

// ValidateReturnReason recorta y valida el motivo: obligatorio, hasta 300.
func ValidateReturnReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	switch {
	case reason == "":
		return "", ErrReturnReasonRequired
	case utf8.RuneCountInString(reason) > ReturnReasonMaxLen:
		return "", ErrReturnReasonTooLong
	}
	return reason, nil
}
