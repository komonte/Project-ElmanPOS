package model

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/shopspring/decimal"
)

const (
	ProductSKUMaxLen         = 40
	ProductNameMinLen        = 2
	ProductNameMaxLen        = 200
	ProductDescriptionMaxLen = 500
)

// SaleType dice cómo se vende un producto (02 §3, enum product_sale_type).
type SaleType string

const (
	SaleTypeUnit      SaleType = "UNIT"      // por unidad
	SaleTypeWeight    SaleType = "WEIGHT"    // a granel: precio y cantidad por kg
	SaleTypeComposite SaleType = "COMPOSITE" // combo: descuenta stock de sus componentes
)

// MeasureUnit es la unidad en que se cuenta el stock (02 §3, enum measure_unit).
type MeasureUnit string

const (
	MeasureUnitUnit       MeasureUnit = "UNIT"
	MeasureUnitGram       MeasureUnit = "GRAM"
	MeasureUnitKilogram   MeasureUnit = "KILOGRAM"
	MeasureUnitLiter      MeasureUnit = "LITER"
	MeasureUnitMilliliter MeasureUnit = "MILLILITER"
	MeasureUnitMeter      MeasureUnit = "METER"
)

// SaleTypes y MeasureUnits son los valores válidos, en el orden en que se
// muestran en un select. Tienen que coincidir con los ENUM de la DB.
var (
	SaleTypes    = []SaleType{SaleTypeUnit, SaleTypeWeight, SaleTypeComposite}
	MeasureUnits = []MeasureUnit{
		MeasureUnitUnit, MeasureUnitGram, MeasureUnitKilogram,
		MeasureUnitLiter, MeasureUnitMilliliter, MeasureUnitMeter,
	}
)

var (
	ErrNilProduct = errors.New("product cannot be nil")

	ErrEmptyProductSKU   = errors.New("product sku is required")
	ErrProductSKUTooLong = errors.New("product sku cannot exceed 40 characters")

	ErrEmptyProductName    = errors.New("product name is required")
	ErrProductNameTooShort = errors.New("product name must be at least 2 characters")
	ErrProductNameTooLong  = errors.New("product name cannot exceed 200 characters")

	ErrProductDescriptionTooLong = errors.New("product description cannot exceed 500 characters")

	ErrProductCategoryRequired = errors.New("product category is required")
	ErrInvalidProductBrand     = errors.New("product brand id must be positive")
	ErrInvalidProductSupplier  = errors.New("product preferred supplier id must be positive")

	ErrInvalidSaleType     = errors.New("product sale type must be UNIT, WEIGHT or COMPOSITE")
	ErrInvalidMeasureUnit  = errors.New("product measure unit must be UNIT, GRAM, KILOGRAM, LITER, MILLILITER or METER")
	ErrWeightNeedsKilogram = errors.New("products sold by weight must be measured in KILOGRAM")
	ErrCompositeBatches    = errors.New("composite products cannot control batches")

	ErrInvalidReorderPoint = errors.New("product reorder point must be between 0 and 999999999.999, with up to 3 decimals")
	ErrInvalidMaxStock     = errors.New("product max stock must be greater than the reorder point, up to 999999999.999, with up to 3 decimals")
	ErrInvalidMargin       = errors.New("product margin must be at least 0 and less than 100, with up to 2 decimals")
)

var (
	// NUMERIC(12,3): 9 dígitos enteros y 3 decimales.
	maxQuantity = decimal.RequireFromString("999999999.999")
	maxMargin   = decimal.NewFromInt(100)
)

// Product es un artículo del catálogo (02 4.4). Los códigos de barra
// (product_codes) y los precios (prices) son tablas aparte.
//
// Los opcionales que son FK (marca, proveedor) y MaxStock son punteros:
// nil es "sin dato" (NULL en la DB). Description es string: vacío es NULL.
type Product struct {
	ID                  int64            `json:"id" db:"id"`
	SKU                 string           `json:"sku" db:"sku"`
	Name                string           `json:"name" db:"name"`
	Description         string           `json:"description" db:"description"`
	BrandID             *int64           `json:"brand_id" db:"brand_id"`
	CategoryID          int64            `json:"category_id" db:"category_id"`
	MeasureUnit         MeasureUnit      `json:"measure_unit" db:"measure_unit"`
	SaleType            SaleType         `json:"sale_type" db:"sale_type"`
	ControlsBatches     bool             `json:"controls_batches" db:"controls_batches"`
	ReorderPoint        decimal.Decimal  `json:"reorder_point" db:"reorder_point"`
	MaxStock            *decimal.Decimal `json:"max_stock" db:"max_stock"`
	MarginPercent       decimal.Decimal  `json:"margin_percent" db:"margin_percent"`
	PreferredSupplierID *int64           `json:"preferred_supplier_id" db:"preferred_supplier_id"`
	IsConsignment       bool             `json:"is_consignment" db:"is_consignment"`
	IsActive            bool             `json:"is_active" db:"is_active"`

	// Solo lectura: el store los llena con un JOIN. Create y Update los ignoran.
	BrandName    string `json:"brand_name,omitempty" db:"-"`
	CategoryName string `json:"category_name,omitempty" db:"-"`
	SupplierName string `json:"supplier_name,omitempty" db:"-"`

	CreatedAt string `json:"created_at" db:"created_at"`
	UpdatedAt string `json:"updated_at" db:"updated_at"`
}

// Validate normaliza y valida los datos que escribe el usuario. No revisa que
// la marca, la categoría o el proveedor existan o estén activos, ni si el
// producto ya tiene movimientos: eso necesita la DB y lo hace el store.
func (p *Product) Validate() error {
	if p == nil {
		return ErrNilProduct
	}

	// SKU: " abc-01 " -> "ABC-01". El UNIQUE de la DB distingue mayúsculas,
	// así que se guarda siempre igual para que "abc-01" no entre como otro.
	p.SKU = strings.ToUpper(strings.Join(strings.Fields(p.SKU), " "))
	if p.SKU == "" {
		return ErrEmptyProductSKU
	}
	if utf8.RuneCountInString(p.SKU) > ProductSKUMaxLen {
		return ErrProductSKUTooLong
	}

	// Name: en mayúsculas, como las marcas y el set de datos real.
	p.Name = strings.ToUpper(strings.Join(strings.Fields(p.Name), " "))
	if p.Name == "" {
		return ErrEmptyProductName
	}
	nameLen := utf8.RuneCountInString(p.Name)
	if nameLen < ProductNameMinLen {
		return ErrProductNameTooShort
	}
	if nameLen > ProductNameMaxLen {
		return ErrProductNameTooLong
	}

	// Description: se ordenan los espacios, pero se respetan las mayúsculas.
	p.Description = strings.Join(strings.Fields(p.Description), " ")
	if utf8.RuneCountInString(p.Description) > ProductDescriptionMaxLen {
		return ErrProductDescriptionTooLong
	}

	// Categoría obligatoria (NOT NULL): 0 es "no se eligió".
	if p.CategoryID <= 0 {
		return ErrProductCategoryRequired
	}
	// Marca y proveedor son opcionales: "sin dato" es nil, no 0.
	if p.BrandID != nil && *p.BrandID <= 0 {
		return ErrInvalidProductBrand
	}
	if p.PreferredSupplierID != nil && *p.PreferredSupplierID <= 0 {
		return ErrInvalidProductSupplier
	}

	// Enums: vacío toma el mismo default que la DB ('UNIT'). Se aceptan en
	// minúsculas ("weight") porque la DB solo acepta el valor exacto.
	p.SaleType = SaleType(strings.ToUpper(strings.TrimSpace(string(p.SaleType))))
	if p.SaleType == "" {
		p.SaleType = SaleTypeUnit
	}
	if !slices.Contains(SaleTypes, p.SaleType) {
		return ErrInvalidSaleType
	}
	p.MeasureUnit = MeasureUnit(strings.ToUpper(strings.TrimSpace(string(p.MeasureUnit))))
	if p.MeasureUnit == "" {
		p.MeasureUnit = MeasureUnitUnit
	}
	if !slices.Contains(MeasureUnits, p.MeasureUnit) {
		return ErrInvalidMeasureUnit
	}
	// A granel el precio es por kg y la cantidad vendida está en kg (F-10).
	if p.SaleType == SaleTypeWeight && p.MeasureUnit != MeasureUnitKilogram {
		return ErrWeightNeedsKilogram
	}
	// Un combo no tiene stock propio: vender uno descuenta el de sus
	// componentes (02 4.6). Los lotes, si hay, son de cada componente.
	if p.SaleType == SaleTypeComposite && p.ControlsBatches {
		return ErrCompositeBatches
	}

	// Cantidades: NUMERIC(12,3). Postgres redondearía en silencio un cuarto
	// decimal; mejor avisar que guardar otro número.
	if !validQuantity(p.ReorderPoint) {
		return ErrInvalidReorderPoint
	}
	if p.MaxStock != nil {
		if !validQuantity(*p.MaxStock) || !p.MaxStock.GreaterThan(p.ReorderPoint) {
			return ErrInvalidMaxStock
		}
	}

	// Margen: menor que 100, porque el PVP sugerido es costo / (1 - margen/100)
	// y con 100 se dividiría por cero.
	if p.MarginPercent.IsNegative() || !p.MarginPercent.LessThan(maxMargin) ||
		!p.MarginPercent.Equal(p.MarginPercent.Round(2)) {
		return ErrInvalidMargin
	}

	return nil
}

// validQuantity: entre 0 y el máximo de NUMERIC(12,3), con hasta 3 decimales.
// "1.500" vale (sobran ceros, no decimales); "1.5001" no.
func validQuantity(d decimal.Decimal) bool {
	return !d.IsNegative() && !d.GreaterThan(maxQuantity) && d.Equal(d.Round(3))
}
