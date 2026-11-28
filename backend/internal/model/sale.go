package model

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

const (
	PaymentReferenceMaxLen = 120 // VARCHAR(120)
	VoidReasonMaxLen       = 300 // VARCHAR(300)
)

// Estados de una venta (ENUM sale_status). Una venta solo se deshace
// entera, anulándola (F-08).
const (
	SaleCompleted = "COMPLETED"
	SaleVoided    = "VOIDED"
)

// Medios de pago (ENUM payment_method).
const (
	PaymentCash     = "CASH"
	PaymentDebit    = "DEBIT_CARD"
	PaymentCredit   = "CREDIT_CARD"
	PaymentTransfer = "TRANSFER"
	PaymentQR       = "QR"
)

// PaymentMethods son los medios que se ofrecen al cobrar, en el orden en que
// se muestran.
var PaymentMethods = []string{PaymentCash, PaymentDebit, PaymentCredit, PaymentTransfer, PaymentQR}

var (
	ErrNilSale                 = errors.New("sale cannot be nil")
	ErrSaleNoItems             = errors.New("sale needs at least one line")
	ErrNilSaleItem             = errors.New("sale line cannot be nil")
	ErrSaleItemProduct         = errors.New("sale line product is required")
	ErrSaleDuplicateProduct    = errors.New("sale has the same product in two lines")
	ErrInvalidSaleQuantity     = errors.New("sale quantity must be greater than 0, up to 999999999.999, with up to 3 decimals")
	ErrSaleQuantityNotWhole    = errors.New("a product sold by unit needs a whole quantity")
	ErrSaleWeightTooLarge      = errors.New("weight is above the maximum per line")
	ErrSaleLineTotalZero       = errors.New("sale line total rounds to 0")
	ErrSaleTotalTooLarge       = errors.New("sale total cannot exceed 9999999999.99")
	ErrSaleNoPayments          = errors.New("sale needs at least one payment")
	ErrNilPayment              = errors.New("payment cannot be nil")
	ErrInvalidPaymentMethod    = errors.New("payment method must be CASH, DEBIT_CARD, CREDIT_CARD, TRANSFER or QR")
	ErrInvalidPaymentAmount    = errors.New("payment amount must be greater than 0, up to 9999999999.99, with up to 2 decimals")
	ErrPaymentReferenceTooLong = errors.New("payment reference cannot exceed 120 characters")
	ErrSaleTwoCashPayments     = errors.New("sale can have only one cash payment")
	ErrPaymentsShort           = errors.New("payments do not cover the sale total")
	ErrPaymentsOverTotal       = errors.New("only cash can go over the sale total")
	ErrVoidReasonRequired      = errors.New("void reason is required")
	ErrVoidReasonTooLong       = errors.New("void reason cannot exceed 300 characters")
	ErrSaleAlreadyVoided       = errors.New("sale is already voided")
)

// Sale es una venta (02 4.16, F-07): renglones, pagos y totales. Se guarda
// entera en una transacción o no se guarda (BR-04).
//
// Los precios incluyen IVA: Subtotal es la suma de los renglones, TaxAmount
// es el IVA que ya está adentro y Total = Subtotal − DiscountAmount (por
// ahora sin descuentos).
type Sale struct {
	ID               int64           `json:"id" db:"id"`
	CashierSessionID int64           `json:"cashier_session_id" db:"cashier_session_id"`
	SaleNumber       int             `json:"sale_number" db:"sale_number"` // sigue por caja, no se repite
	Status           string          `json:"status" db:"status"`
	Subtotal         decimal.Decimal `json:"subtotal" db:"subtotal"`
	TaxAmount        decimal.Decimal `json:"tax_amount" db:"tax_amount"`
	DiscountAmount   decimal.Decimal `json:"discount_amount" db:"discount_amount"`
	Total            decimal.Decimal `json:"total" db:"total"`
	CreatedAt        string          `json:"created_at" db:"created_at"`
	// CreatedBy es quién cobró (F-13). nil en las ventas de antes del login.
	CreatedBy *int64 `json:"created_by" db:"created_by"`
	// Anulación (F-08): cuándo y por qué. Nil y vacío si no está anulada
	// (chk_sales_void de la 0018).
	VoidedAt   *string `json:"voided_at" db:"voided_at"`
	VoidReason string  `json:"void_reason" db:"void_reason"`

	Items    []*SaleItem `json:"items" db:"-"`
	Payments []*Payment  `json:"payments" db:"-"`

	// Solo lectura, para el ticket: la caja y el local (dirección vacía si
	// no tiene).
	RegisterName string `json:"register_name" db:"-"`
	StoreName    string `json:"store_name" db:"-"`
	StoreAddress string `json:"store_address" db:"-"`
	// CreatedByName es el nombre de quién cobró (nil si no se sabe).
	CreatedByName *string `json:"created_by_name" db:"-"`

	// Change es el vuelto: lo que el efectivo entregado pasa del total. No
	// se guarda (SettlePayments lo calcula).
	Change decimal.Decimal `json:"change" db:"-"`
}

// SaleItem es un renglón (02 4.17). Precio, costo e IVA son copias del
// momento de la venta (BR-03): un cambio de precio después no la toca.
type SaleItem struct {
	ID        int64           `json:"id" db:"id"`
	SaleID    int64           `json:"sale_id" db:"sale_id"`
	ProductID int64           `json:"product_id" db:"product_id"`
	Quantity  decimal.Decimal `json:"quantity" db:"quantity"`     // unidades o kg
	UnitPrice decimal.Decimal `json:"unit_price" db:"unit_price"` // con IVA; por kg si es a granel
	// UnitCost es el costo promedio de lo que salió: cada lote a su costo y
	// lo que no tiene lote al promedio vigente (el detalle está en Batches).
	UnitCost decimal.Decimal `json:"unit_cost" db:"unit_cost"`
	TaxRate  decimal.Decimal `json:"tax_rate" db:"tax_rate"` // 21 = 21 %
	// Uncovered es cuánto del renglón el sistema no tenía (modo arranque,
	// migración 0021): no salió del stock y queda para contar. 0 casi siempre.
	Uncovered decimal.Decimal `json:"uncovered_quantity" db:"uncovered_quantity"`
	// ScannedCode es el código escaneado que no estaba, en el renglón de un
	// artículo sin registrar (migración 0022), para darlo de alta después.
	// Vacío si no se escaneó nada.
	ScannedCode string `json:"scanned_code,omitempty" db:"scanned_code"`
	CreatedAt   string `json:"created_at" db:"created_at"`

	Batches []*SaleItemBatch `json:"batches" db:"-"`

	// Solo lectura, para el ticket.
	ProductName string   `json:"product_name" db:"-"`
	SaleType    SaleType `json:"sale_type" db:"-"`
}

// SaleItemBatch es cuánto salió de un lote para un renglón (02 4.18, FIFO).
type SaleItemBatch struct {
	ID         int64           `json:"id" db:"id"`
	SaleItemID int64           `json:"sale_item_id" db:"sale_item_id"`
	BatchID    int64           `json:"batch_id" db:"batch_id"`
	Quantity   decimal.Decimal `json:"quantity" db:"quantity"`
	UnitCost   decimal.Decimal `json:"unit_cost" db:"unit_cost"`
}

// Payment es un pago de la venta (02 4.19). Varios pagos dividen el cobro.
// Amount es lo que se aplica a la venta: en efectivo, sin el vuelto.
type Payment struct {
	ID        int64           `json:"id" db:"id"`
	SaleID    int64           `json:"sale_id" db:"sale_id"`
	Method    string          `json:"method" db:"method"`
	Amount    decimal.Decimal `json:"amount" db:"amount"`
	Reference string          `json:"reference" db:"reference"` // vacío es NULL
	CreatedAt string          `json:"created_at" db:"created_at"`

	// Tendered es lo que entregó el cliente en efectivo ("paga con"). No se
	// guarda; SettlePayments lo llena.
	Tendered decimal.Decimal `json:"tendered" db:"-"`
}

// Validate valida lo que manda la caja: productos, cantidades y pagos. Los
// precios, el IVA y los costos los pone el caso de uso desde la DB. El caso
// de uso también revisa la cantidad según el tipo de venta
// (ValidateQuantity) y el stock.
func (s *Sale) Validate() error {
	if s == nil {
		return ErrNilSale
	}
	if len(s.Items) == 0 {
		return ErrSaleNoItems
	}
	seen := make(map[int64]bool, len(s.Items))
	for i, it := range s.Items {
		if err := it.validate(); err != nil {
			return fmt.Errorf("line %d: %w", i+1, err)
		}
		// Un artículo sin registrar (precio escrito en el renglón) puede ir
		// en varios renglones, cada uno con su precio y su código; que el
		// producto lo sea lo revisa el caso de uso.
		if seen[it.ProductID] && !it.UnitPrice.IsPositive() {
			return fmt.Errorf("line %d: %w", i+1, ErrSaleDuplicateProduct)
		}
		seen[it.ProductID] = true
	}
	if len(s.Payments) == 0 {
		return ErrSaleNoPayments
	}
	cash := 0
	for i, p := range s.Payments {
		if err := p.Validate(); err != nil {
			return fmt.Errorf("payment %d: %w", i+1, err)
		}
		if p.Method == PaymentCash {
			cash++
		}
	}
	if cash > 1 {
		return ErrSaleTwoCashPayments
	}
	return nil
}

func (it *SaleItem) validate() error {
	if it == nil {
		return ErrNilSaleItem
	}
	if it.ProductID <= 0 {
		return ErrSaleItemProduct
	}
	if !it.Quantity.IsPositive() || !validQuantity(it.Quantity) {
		return ErrInvalidSaleQuantity
	}
	return nil
}

// ValidateQuantity revisa la cantidad según cómo se vende el producto: por
// unidad, entera; a granel, en kg (hasta 3 decimales, ya revisado) y no más
// que maxWeightKg (app_settings max_sale_weight_kg, F-10).
func (it *SaleItem) ValidateQuantity(saleType SaleType, maxWeightKg decimal.Decimal) error {
	switch saleType {
	case SaleTypeWeight:
		if it.Quantity.GreaterThan(maxWeightKg) {
			return ErrSaleWeightTooLarge
		}
	default:
		if !it.Quantity.IsInteger() {
			return ErrSaleQuantityNotWhole
		}
	}
	return nil
}

// Validate valida un pago: medio, monto y referencia (recortada).
func (p *Payment) Validate() error {
	if p == nil {
		return ErrNilPayment
	}
	if !slices.Contains(PaymentMethods, p.Method) {
		return ErrInvalidPaymentMethod
	}
	if !p.Amount.IsPositive() || !validMoney(p.Amount) {
		return ErrInvalidPaymentAmount
	}
	p.Reference = strings.TrimSpace(p.Reference)
	if utf8.RuneCountInString(p.Reference) > PaymentReferenceMaxLen {
		return ErrPaymentReferenceTooLong
	}
	return nil
}

// LineTotal es cantidad × precio, redondeado a centavos (como en el ticket).
func (it *SaleItem) LineTotal() decimal.Decimal {
	return it.Quantity.Mul(it.UnitPrice).Round(2)
}

// lineTax es el IVA que está adentro del total del renglón, sin redondear:
// total × tasa / (100 + tasa).
func (it *SaleItem) lineTax() decimal.Decimal {
	if !it.TaxRate.IsPositive() {
		return decimal.Zero
	}
	hundred := decimal.NewFromInt(100)
	return it.LineTotal().Mul(it.TaxRate).Div(hundred.Add(it.TaxRate))
}

// ComputeTotals llena Subtotal, TaxAmount, DiscountAmount (0) y Total con
// los precios y tasas de los renglones. El IVA se suma sin redondear y se
// redondea una vez. Un renglón que redondea a $0 no se vende (la DB pide
// total > 0 y no tiene sentido cobrarlo).
func (s *Sale) ComputeTotals() error {
	subtotal, tax := decimal.Zero, decimal.Zero
	for i, it := range s.Items {
		lt := it.LineTotal()
		if !lt.IsPositive() {
			return fmt.Errorf("line %d: %w", i+1, ErrSaleLineTotalZero)
		}
		subtotal = subtotal.Add(lt)
		tax = tax.Add(it.lineTax())
	}
	if subtotal.GreaterThan(maxAmount) {
		return ErrSaleTotalTooLarge
	}
	s.Subtotal = subtotal
	s.TaxAmount = tax.Round(2)
	s.DiscountAmount = decimal.Zero
	s.Total = subtotal
	return nil
}

// PaymentsTotal suma los montos de los pagos.
func (s *Sale) PaymentsTotal() decimal.Decimal {
	sum := decimal.Zero
	for _, p := range s.Payments {
		sum = sum.Add(p.Amount)
	}
	return sum
}

// SettlePayments ajusta los pagos al total (después de ComputeTotals). Los
// pagos tienen que cubrir el total. Solo el efectivo puede pasarse: lo que
// sobra es el vuelto, y el pago en efectivo queda en lo que se aplica a la
// venta (Tendered guarda lo entregado). Si sin el efectivo ya se cubre el
// total, el efectivo sobra entero y se rechaza.
func (s *Sale) SettlePayments() error {
	paid := s.PaymentsTotal()
	if paid.LessThan(s.Total) {
		return ErrPaymentsShort
	}
	var cash *Payment
	for _, p := range s.Payments {
		if p.Method == PaymentCash {
			cash = p
		}
	}
	excess := paid.Sub(s.Total)
	if cash != nil {
		cash.Tendered = cash.Amount
	}
	s.Change = decimal.Zero
	if !excess.IsPositive() {
		return nil
	}
	if cash == nil || !excess.LessThan(cash.Amount) {
		return ErrPaymentsOverTotal
	}
	cash.Amount = cash.Amount.Sub(excess)
	s.Change = excess
	return nil
}

// ValidateVoidReason recorta el motivo de una anulación y lo valida:
// obligatorio y hasta 300 caracteres. Devuelve el motivo recortado.
func ValidateVoidReason(reason string) (string, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "", ErrVoidReasonRequired
	}
	if utf8.RuneCountInString(reason) > VoidReasonMaxLen {
		return "", ErrVoidReasonTooLong
	}
	return reason, nil
}

// CanVoid dice si la venta se puede anular: solo una COMPLETED. Que sea de
// la sesión abierta lo revisa el caso de uso.
func (s *Sale) CanVoid() error {
	if s == nil {
		return ErrNilSale
	}
	if s.Status == SaleVoided {
		return ErrSaleAlreadyVoided
	}
	return nil
}
