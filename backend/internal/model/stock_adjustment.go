package model

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

const StockAdjustmentNotesMaxLen = 500 // VARCHAR(500)

// Motivos de una salida conocida (F-09b, ENUM adjustment_reason). El de
// conteo (ReasonCount) lo pone la confirmación de un conteo; THEFT no se
// ofrece: una pérdida sin explicación aparece en un conteo.
const (
	ReasonOwnConsumption = "OWN_CONSUMPTION"
	ReasonDamage         = "DAMAGE"
	ReasonExpiration     = "EXPIRATION"
	ReasonOther          = "OTHER"
)

// ExitReasons son los motivos que se eligen al anotar una salida, en el orden
// en que se muestran.
var ExitReasons = []string{ReasonOwnConsumption, ReasonDamage, ReasonExpiration, ReasonOther}

var (
	ErrNilStockAdjustment        = errors.New("stock adjustment cannot be nil")
	ErrStockAdjustmentProduct    = errors.New("stock adjustment product is required")
	ErrInvalidAdjustmentReason   = errors.New("stock adjustment reason must be OWN_CONSUMPTION, DAMAGE, EXPIRATION or OTHER")
	ErrInvalidAdjustmentQuantity = errors.New("stock adjustment quantity must be greater than 0, up to 999999999.999, with up to 3 decimals")
	ErrAdjustmentNotesTooLong    = errors.New("stock adjustment notes cannot exceed 500 characters")
	ErrAdjustmentNotesRequired   = errors.New("stock adjustment with reason OTHER needs notes")
	ErrInvalidAdjustmentBatch    = errors.New("stock adjustment batch is invalid")
)

// StockAdjustment es una salida conocida de mercadería (02 4.26, F-09b):
// consumo propio, rotura, vencido u otra, anotada cualquier día. Es el origen
// de sus movimientos de stock (ADJUSTMENT, negativos): uno por lote del que
// sale, o uno sin lote. Solo se agregan filas: no se edita ni se borra.
type StockAdjustment struct {
	ID        int64           `json:"id" db:"id"`
	StoreID   int64           `json:"store_id" db:"store_id"`
	ProductID int64           `json:"product_id" db:"product_id"`
	Reason    string          `json:"reason" db:"reason"`
	Quantity  decimal.Decimal `json:"quantity" db:"quantity"` // lo que salió, positivo
	Notes     string          `json:"notes" db:"notes"`       // vacío es NULL

	// BatchID es el lote elegido (por ejemplo, el que venció). Solo lo manda
	// quien anota: sin lote, sale de los lotes en orden FIFO. No se guarda en
	// esta tabla: queda en los movimientos.
	BatchID *int64 `json:"batch_id,omitempty" db:"-"`

	// CreatedBy es quién la anotó (F-13). nil en las de antes del login.
	CreatedBy *int64 `json:"created_by" db:"created_by"`
	CreatedAt string `json:"created_at" db:"created_at"`

	// Solo lectura, para la lista.
	ProductName string `json:"product_name" db:"-"`
	// CreatedByName es el nombre de quién la anotó (nil si no se sabe).
	CreatedByName *string `json:"created_by_name" db:"-"`
}

// Validate normaliza (recorta la nota) y valida lo que se escribe. No revisa
// que el producto exista ni que haya stock: eso lo hace el caso de uso.
func (a *StockAdjustment) Validate() error {
	if a == nil {
		return ErrNilStockAdjustment
	}
	if a.ProductID <= 0 {
		return ErrStockAdjustmentProduct
	}
	if !slices.Contains(ExitReasons, a.Reason) {
		return ErrInvalidAdjustmentReason
	}
	if !a.Quantity.IsPositive() || !validQuantity(a.Quantity) {
		return ErrInvalidAdjustmentQuantity
	}
	a.Notes = strings.TrimSpace(a.Notes)
	if utf8.RuneCountInString(a.Notes) > StockAdjustmentNotesMaxLen {
		return ErrAdjustmentNotesTooLong
	}
	if a.Reason == ReasonOther && a.Notes == "" {
		return ErrAdjustmentNotesRequired
	}
	if a.BatchID != nil && *a.BatchID <= 0 {
		return ErrInvalidAdjustmentBatch
	}
	return nil
}
