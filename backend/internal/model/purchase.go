package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

const (
	PurchaseVoucherMaxLen = 60 // VARCHAR(60)
	BatchNumberMaxLen     = 80 // VARCHAR(80) en product_batches
)

var (
	ErrNilPurchase             = errors.New("purchase cannot be nil")
	ErrPurchaseSupplierMissing = errors.New("purchase supplier is required")
	ErrPurchaseVoucherTooLong  = errors.New("purchase voucher number cannot exceed 60 characters")
	ErrInvalidPurchaseTotal    = errors.New("purchase total must be between 0 and 9999999999.99, with up to 2 decimals")
	ErrPurchaseNoItems         = errors.New("purchase must have at least one line")

	ErrNilPurchaseItem         = errors.New("purchase line cannot be nil")
	ErrPurchaseItemProduct     = errors.New("purchase line product is required")
	ErrInvalidPurchaseQuantity = errors.New("purchase line quantity must be greater than 0, up to 999999999.999, with up to 3 decimals")
	ErrBatchNumberTooLong      = errors.New("batch number cannot exceed 80 characters")
	ErrInvalidExpiryDate       = errors.New("expiry date must be a valid date (YYYY-MM-DD)")
	ErrExpiryWithoutBatch      = errors.New("expiry date needs a batch number")
)

// NUMERIC(12,2), igual que precios y costos.
var maxAmount = decimal.RequireFromString("9999999999.99")

// Purchase es una recepción de mercadería con su factura o remito (02 4.21,
// F-06). Se guarda entera o nada: cabecera, renglones, lotes, movimientos de
// stock, saldos y costos, en una transacción (usecase).
type Purchase struct {
	ID            int64  `json:"id" db:"id"`
	SupplierID    int64  `json:"supplier_id" db:"supplier_id"`
	VoucherNumber string `json:"voucher_number" db:"voucher_number"` // vacío es NULL

	// Total es el subtotal sin IVA que dice la factura, escrito por el
	// usuario. Los costos de los renglones van sin IVA (F-04), así que este
	// total se compara con la suma de los renglones (LinesTotal) para
	// detectar errores de carga. Si no coinciden es un aviso, no un error:
	// puede haber descuentos o redondeos del proveedor.
	Total decimal.Decimal `json:"total" db:"total"`

	Items []*PurchaseItem `json:"items" db:"-"`

	// Solo lectura: los pone el store.
	Status     string `json:"status" db:"status"`
	ReceivedAt string `json:"received_at" db:"received_at"`
	// ReceivedBy queda en nil hasta que haya login (F-13, migración 0013).
	ReceivedBy *int64 `json:"received_by" db:"received_by"`
	CreatedAt  string `json:"created_at" db:"created_at"`
	UpdatedAt  string `json:"updated_at" db:"updated_at"`
}

// PurchaseItem es un renglón de la compra (02 4.22): un producto, cuánto
// entró y a qué costo por unidad (o por kg), sin IVA.
//
// BatchNumber y ExpiryDate solo se usan si el producto controla lotes: el
// caso de uso lo revisa, porque necesita leer el producto.
type PurchaseItem struct {
	ID         int64           `json:"id" db:"id"`
	PurchaseID int64           `json:"purchase_id" db:"purchase_id"`
	ProductID  int64           `json:"product_id" db:"product_id"`
	Quantity   decimal.Decimal `json:"quantity" db:"quantity"`
	UnitCost   decimal.Decimal `json:"unit_cost" db:"unit_cost"`

	BatchNumber string `json:"batch_number,omitempty" db:"-"`
	ExpiryDate  string `json:"expiry_date,omitempty" db:"-"` // "2026-12-31"; vacío si no vence

	// Solo lectura: el lote que se creó (o al que se sumó) al recibir.
	BatchID   *int64 `json:"batch_id" db:"batch_id"`
	CreatedAt string `json:"created_at" db:"created_at"`
}

// Validate normaliza y valida lo que escribe el usuario. No revisa que el
// proveedor y los productos existan y estén activos, ni los lotes según el
// producto: eso necesita la DB y lo hace el caso de uso.
//
// El error de un renglón dice cuál es ("line 2: ..."), contando desde 1 como
// se ven en la pantalla, y envuelve el error del modelo (errors.Is anda).
func (p *Purchase) Validate() error {
	if p == nil {
		return ErrNilPurchase
	}
	if p.SupplierID <= 0 {
		return ErrPurchaseSupplierMissing
	}
	p.VoucherNumber = strings.TrimSpace(p.VoucherNumber)
	if utf8.RuneCountInString(p.VoucherNumber) > PurchaseVoucherMaxLen {
		return ErrPurchaseVoucherTooLong
	}
	if p.Total.IsNegative() || p.Total.GreaterThan(maxAmount) || !p.Total.Equal(p.Total.Round(2)) {
		return ErrInvalidPurchaseTotal
	}
	if len(p.Items) == 0 {
		return ErrPurchaseNoItems
	}
	for i, it := range p.Items {
		if err := it.Validate(); err != nil {
			return fmt.Errorf("line %d: %w", i+1, err)
		}
	}
	return nil
}

// Validate valida un renglón. El costo usa las mismas reglas que un costo
// cargado a mano (Cost.Validate).
func (it *PurchaseItem) Validate() error {
	if it == nil {
		return ErrNilPurchaseItem
	}
	if it.ProductID <= 0 {
		return ErrPurchaseItemProduct
	}
	if !it.Quantity.IsPositive() || !validQuantity(it.Quantity) {
		return ErrInvalidPurchaseQuantity
	}
	if err := (&Cost{UnitCost: it.UnitCost}).Validate(); err != nil {
		return err
	}

	it.BatchNumber = strings.TrimSpace(it.BatchNumber)
	if utf8.RuneCountInString(it.BatchNumber) > BatchNumberMaxLen {
		return ErrBatchNumberTooLong
	}
	it.ExpiryDate = strings.TrimSpace(it.ExpiryDate)
	if it.ExpiryDate != "" {
		if _, err := time.Parse(time.DateOnly, it.ExpiryDate); err != nil {
			return ErrInvalidExpiryDate
		}
		// product_batches.batch_number es NOT NULL: un vencimiento suelto no
		// tiene dónde guardarse.
		if it.BatchNumber == "" {
			return ErrExpiryWithoutBatch
		}
	}
	return nil
}

// LineTotal es cantidad × costo del renglón, redondeado a centavos.
func (it *PurchaseItem) LineTotal() decimal.Decimal {
	return it.Quantity.Mul(it.UnitCost).Round(2)
}

// LinesTotal suma los renglones (cada uno redondeado, como en una factura).
func (p *Purchase) LinesTotal() decimal.Decimal {
	sum := decimal.Zero
	for _, it := range p.Items {
		sum = sum.Add(it.LineTotal())
	}
	return sum
}

// TotalDifference es el total escrito menos la suma de los renglones: 0 si
// coinciden. Positivo: la factura dice más de lo cargado (falta un renglón o
// un costo quedó bajo). La pantalla lo muestra como aviso antes de confirmar.
func (p *Purchase) TotalDifference() decimal.Decimal {
	return p.Total.Sub(p.LinesTotal())
}
