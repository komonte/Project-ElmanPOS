package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/shopspring/decimal"
)

// dec arma un decimal desde un texto; decPtr arma un puntero para MaxStock.
func dec(s string) decimal.Decimal     { return decimal.RequireFromString(s) }
func decPtr(s string) *decimal.Decimal { return new(dec(s)) }

// validProduct es un producto mínimo que pasa Validate. Cada caso cambia
// solo lo que quiere probar.
func validProduct() model.Product {
	return model.Product{SKU: "OREO-200", Name: "GALLETITAS OREO 200G", CategoryID: 1}
}

func TestProductValidate(t *testing.T) {
	tests := []struct {
		name   string
		modify func(p *model.Product)
		// Error esperado. Si es nil, check revisa cómo quedó el producto.
		wantErr error
		check   func(t *testing.T, p model.Product)
	}{
		{
			name:   "minimal product gets the DB defaults",
			modify: func(p *model.Product) {},
			check: func(t *testing.T, p model.Product) {
				if p.SaleType != model.SaleTypeUnit || p.MeasureUnit != model.MeasureUnitUnit {
					t.Errorf("enums: got %q/%q, want UNIT/UNIT", p.SaleType, p.MeasureUnit)
				}
			},
		},
		{
			name: "sku and name are tidied and uppercased",
			modify: func(p *model.Product) {
				p.SKU = "  oreo-200 "
				p.Name = "  galletitas   oreo 200g "
			},
			check: func(t *testing.T, p model.Product) {
				if p.SKU != "OREO-200" || p.Name != "GALLETITAS OREO 200G" {
					t.Errorf("got %q/%q", p.SKU, p.Name)
				}
			},
		},
		{
			name:   "accents and ñ in uppercase",
			modify: func(p *model.Product) { p.Name = "yerba ñandú té" },
			check: func(t *testing.T, p model.Product) {
				if p.Name != "YERBA ÑANDÚ TÉ" {
					t.Errorf("name: got %q", p.Name)
				}
			},
		},
		{
			name:   "description keeps its case",
			modify: func(p *model.Product) { p.Description = "  Paquete   de 3 " },
			check: func(t *testing.T, p model.Product) {
				if p.Description != "Paquete de 3" {
					t.Errorf("description: got %q", p.Description)
				}
			},
		},
		{
			name: "enums in lowercase are accepted",
			modify: func(p *model.Product) {
				p.SaleType = " weight "
				p.MeasureUnit = "kilogram"
			},
			check: func(t *testing.T, p model.Product) {
				if p.SaleType != model.SaleTypeWeight || p.MeasureUnit != model.MeasureUnitKilogram {
					t.Errorf("enums: got %q/%q", p.SaleType, p.MeasureUnit)
				}
			},
		},
		{
			name: "full product",
			modify: func(p *model.Product) {
				p.BrandID = new(int64(2))
				p.PreferredSupplierID = new(int64(1))
				p.SaleType = model.SaleTypeComposite
				p.ReorderPoint = dec("10")
				p.MaxStock = decPtr("50.5")
				p.MarginPercent = dec("35.25")
			},
		},
		{
			name: "limits are allowed",
			modify: func(p *model.Product) {
				p.SKU = strings.Repeat("A", model.ProductSKUMaxLen)
				p.Name = strings.Repeat("A", model.ProductNameMaxLen)
				p.Description = strings.Repeat("a", model.ProductDescriptionMaxLen)
				p.ReorderPoint = dec("999999999.998")
				p.MaxStock = decPtr("999999999.999")
				p.MarginPercent = dec("99.99")
			},
		},
		{
			name:   "trailing zeros are not extra decimals",
			modify: func(p *model.Product) { p.ReorderPoint = dec("1.5000") },
		},

		// SKU
		{
			name:   "empty sku is left for the DB to generate",
			modify: func(p *model.Product) { p.SKU = "  " },
			check: func(t *testing.T, p model.Product) {
				if p.SKU != "" {
					t.Errorf("sku: got %q, want empty", p.SKU)
				}
			},
		},
		{
			name:    "sku too long",
			modify:  func(p *model.Product) { p.SKU = strings.Repeat("A", model.ProductSKUMaxLen+1) },
			wantErr: model.ErrProductSKUTooLong,
		},

		// Name y description
		{name: "empty name", modify: func(p *model.Product) { p.Name = " " }, wantErr: model.ErrEmptyProductName},
		{name: "name too short", modify: func(p *model.Product) { p.Name = "a" }, wantErr: model.ErrProductNameTooShort},
		{
			name:    "name too long",
			modify:  func(p *model.Product) { p.Name = strings.Repeat("A", model.ProductNameMaxLen+1) },
			wantErr: model.ErrProductNameTooLong,
		},
		{
			name:    "description too long",
			modify:  func(p *model.Product) { p.Description = strings.Repeat("a", model.ProductDescriptionMaxLen+1) },
			wantErr: model.ErrProductDescriptionTooLong,
		},

		// Referencias
		{name: "missing category", modify: func(p *model.Product) { p.CategoryID = 0 }, wantErr: model.ErrProductCategoryRequired},
		{name: "negative category", modify: func(p *model.Product) { p.CategoryID = -1 }, wantErr: model.ErrProductCategoryRequired},
		{name: "brand zero is not 'no brand'", modify: func(p *model.Product) { p.BrandID = new(int64(0)) }, wantErr: model.ErrInvalidProductBrand},
		{name: "supplier zero is not 'no supplier'", modify: func(p *model.Product) { p.PreferredSupplierID = new(int64(0)) }, wantErr: model.ErrInvalidProductSupplier},

		// Enums
		{name: "unknown sale type", modify: func(p *model.Product) { p.SaleType = "BOX" }, wantErr: model.ErrInvalidSaleType},
		{name: "unknown measure unit", modify: func(p *model.Product) { p.MeasureUnit = "POUND" }, wantErr: model.ErrInvalidMeasureUnit},
		{
			name:    "weight measured in units",
			modify:  func(p *model.Product) { p.SaleType = model.SaleTypeWeight },
			wantErr: model.ErrWeightNeedsKilogram,
		},
		{
			name: "weight measured in grams",
			modify: func(p *model.Product) {
				p.SaleType = model.SaleTypeWeight
				p.MeasureUnit = model.MeasureUnitGram
			},
			wantErr: model.ErrWeightNeedsKilogram,
		},
		{
			name: "composite with batches",
			modify: func(p *model.Product) {
				p.SaleType = model.SaleTypeComposite
				p.ControlsBatches = true
			},
			wantErr: model.ErrCompositeBatches,
		},
		{
			name: "unit product with batches",
			modify: func(p *model.Product) {
				p.ControlsBatches = true
			},
		},

		// Reorder point y max stock
		{name: "negative reorder point", modify: func(p *model.Product) { p.ReorderPoint = dec("-1") }, wantErr: model.ErrInvalidReorderPoint},
		{name: "reorder point with 4 decimals", modify: func(p *model.Product) { p.ReorderPoint = dec("0.0005") }, wantErr: model.ErrInvalidReorderPoint},
		{name: "reorder point too big", modify: func(p *model.Product) { p.ReorderPoint = dec("1000000000") }, wantErr: model.ErrInvalidReorderPoint},
		{
			name: "max stock equal to reorder point",
			modify: func(p *model.Product) {
				p.ReorderPoint = dec("10")
				p.MaxStock = decPtr("10")
			},
			wantErr: model.ErrInvalidMaxStock,
		},
		{
			name: "max stock below reorder point",
			modify: func(p *model.Product) {
				p.ReorderPoint = dec("10")
				p.MaxStock = decPtr("5")
			},
			wantErr: model.ErrInvalidMaxStock,
		},
		{name: "max stock zero", modify: func(p *model.Product) { p.MaxStock = decPtr("0") }, wantErr: model.ErrInvalidMaxStock},
		{name: "max stock with 4 decimals", modify: func(p *model.Product) { p.MaxStock = decPtr("1.0001") }, wantErr: model.ErrInvalidMaxStock},
		{name: "max stock too big", modify: func(p *model.Product) { p.MaxStock = decPtr("1000000000") }, wantErr: model.ErrInvalidMaxStock},

		// Margen
		{name: "negative margin", modify: func(p *model.Product) { p.MarginPercent = dec("-0.01") }, wantErr: model.ErrInvalidMargin},
		{name: "margin 100 divides by zero", modify: func(p *model.Product) { p.MarginPercent = dec("100") }, wantErr: model.ErrInvalidMargin},
		{name: "margin with 3 decimals", modify: func(p *model.Product) { p.MarginPercent = dec("10.125") }, wantErr: model.ErrInvalidMargin},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validProduct()
			tt.modify(&p)
			err := p.Validate()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && tt.check != nil {
				tt.check(t, p)
			}
		})
	}
}

func TestProductValidateNil(t *testing.T) {
	var p *model.Product
	if err := p.Validate(); !errors.Is(err, model.ErrNilProduct) {
		t.Fatalf("error: got %v, want ErrNilProduct", err)
	}
}
