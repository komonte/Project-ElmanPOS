package model

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

const StockCountSectorMaxLen = 60 // VARCHAR(60) en stock_count_entries

// Estados de un conteo (ENUM count_status). DRAFT → CONFIRMED es de una sola
// vía: un conteo confirmado ya movió el stock.
const (
	CountDraft     = "DRAFT"
	CountConfirmed = "CONFIRMED"
)

var (
	ErrNilStockCountEntry     = errors.New("stock count entry cannot be nil")
	ErrStockCountEntryProduct = errors.New("stock count entry product is required")
	ErrStockCountSectorEmpty  = errors.New("stock count sector is required")
	ErrStockCountSectorLong   = errors.New("stock count sector cannot exceed 60 characters")
	ErrInvalidCountedQuantity = errors.New("counted quantity must be between 0 and 999999999.999, with up to 3 decimals")

	ErrNilStockCountItem = errors.New("stock count item cannot be nil")
)

// StockCount es un conteo físico de stock (02 4.14, F-09). También sirve de
// inventario inicial: el stock que ya está en el local no se carga como
// compra, se cuenta.
//
// Se cuenta en borrador (DRAFT), por sectores y de a poco. Al confirmar, cada
// producto contado cuya cantidad difiere de la del sistema recibe un
// movimiento ADJUSTMENT/COUNT. Los productos que no se contaron no se tocan.
type StockCount struct {
	ID      int64  `json:"id" db:"id"`
	StoreID int64  `json:"store_id" db:"store_id"`
	Status  string `json:"status" db:"status"`
	// CreatedBy queda en nil hasta que haya login (F-13, migración 0015).
	CreatedBy   *int64  `json:"created_by" db:"created_by"`
	CreatedAt   string  `json:"created_at" db:"created_at"`
	UpdatedAt   string  `json:"updated_at" db:"updated_at"`
	ConfirmedAt *string `json:"confirmed_at" db:"confirmed_at"`

	Items []*StockCountItem `json:"items" db:"-"`
}

// StockCountItem es un producto contado: un renglón por producto y conteo.
//
// SystemQuantity es la foto del saldo cuando el producto se cargó por primera
// vez en el conteo; CountedQuantity, la suma de sus cargas (Entries). Al
// confirmar se mueve la diferencia (Difference), no se pisa el saldo: si
// mientras tanto entró una compra, no se pierde.
//
// UnitCost y ExpiryDate son opcionales y solo se usan si sobra mercadería
// (diferencia positiva): el costo entra al promedio (F-04) y el vencimiento
// va al lote que se crea si el producto controla lotes.
type StockCountItem struct {
	ID        int64 `json:"id" db:"id"`
	CountID   int64 `json:"count_id" db:"count_id"`
	ProductID int64 `json:"product_id" db:"product_id"`

	SystemQuantity  decimal.Decimal `json:"system_quantity" db:"system_quantity"`
	CountedQuantity decimal.Decimal `json:"counted_quantity" db:"counted_quantity"`

	UnitCost   *decimal.Decimal `json:"unit_cost" db:"unit_cost"`     // nil = sin costo
	ExpiryDate string           `json:"expiry_date" db:"expiry_date"` // "2027-03-31"; vacío si no vence

	// Solo lectura, para la pantalla.
	ProductName string             `json:"product_name" db:"-"`
	Entries     []*StockCountEntry `json:"entries" db:"-"`
	CreatedAt   string             `json:"created_at" db:"created_at"`
	UpdatedAt   string             `json:"updated_at" db:"updated_at"`
}

// StockCountEntry es una carga: un producto contado en un sector ("Góndola
// 3", "Depósito"). El mismo producto en dos sectores son dos cargas que se
// suman. Cero vale: "miré acá y no hay".
type StockCountEntry struct {
	ID     int64 `json:"id" db:"id"`
	ItemID int64 `json:"item_id" db:"item_id"`
	// ProductID es lo que manda quien carga; el caso de uso busca (o crea)
	// el renglón del producto y completa ItemID.
	ProductID int64           `json:"product_id" db:"-"`
	Sector    string          `json:"sector" db:"sector"`
	Quantity  decimal.Decimal `json:"quantity" db:"quantity"`
	CreatedAt string          `json:"created_at" db:"created_at"`
}

// Validate normaliza (recorta el sector) y valida una carga. No revisa que
// el producto exista ni que se pueda contar: eso lo hace el caso de uso.
func (e *StockCountEntry) Validate() error {
	if e == nil {
		return ErrNilStockCountEntry
	}
	if e.ProductID <= 0 {
		return ErrStockCountEntryProduct
	}
	e.Sector = strings.Join(strings.Fields(e.Sector), " ")
	if e.Sector == "" {
		return ErrStockCountSectorEmpty
	}
	if utf8.RuneCountInString(e.Sector) > StockCountSectorMaxLen {
		return ErrStockCountSectorLong
	}
	if !validQuantity(e.Quantity) {
		return ErrInvalidCountedQuantity
	}
	return nil
}

// Validate valida lo que se escribe a mano en un renglón: el costo (mismas
// reglas que un costo cargado a mano, Cost.Validate) y el vencimiento. Las
// cantidades las pone el sistema.
func (it *StockCountItem) Validate() error {
	if it == nil {
		return ErrNilStockCountItem
	}
	if it.UnitCost != nil {
		if err := (&Cost{UnitCost: *it.UnitCost}).Validate(); err != nil {
			return err
		}
	}
	it.ExpiryDate = strings.TrimSpace(it.ExpiryDate)
	if it.ExpiryDate != "" {
		if _, err := time.Parse(time.DateOnly, it.ExpiryDate); err != nil {
			return ErrInvalidExpiryDate
		}
	}
	return nil
}

// Difference es lo contado menos la foto del sistema: positivo si sobra
// mercadería, negativo si falta, cero si coincide (no genera movimiento).
func (it *StockCountItem) Difference() decimal.Decimal {
	return it.CountedQuantity.Sub(it.SystemQuantity)
}
