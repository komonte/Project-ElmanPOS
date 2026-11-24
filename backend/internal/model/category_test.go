package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

// ParentID se arma con new(int64(3)): desde Go 1.26, new acepta una
// expresión y devuelve un puntero a su valor.
func TestCategoryValidate(t *testing.T) {
	tests := []struct {
		name     string
		category model.Category
		wantErr  error
		wantName string // nombre esperado después de normalizar
	}{
		{
			name:     "root category",
			category: model.Category{Name: "Limpieza", TaxID: 1},
			wantName: "Limpieza",
		},
		{
			name:     "subcategory",
			category: model.Category{Name: "Jabones", TaxID: 1, ParentID: new(int64(3))},
			wantName: "Jabones",
		},
		{
			name:     "spaces are tidied, case is kept",
			category: model.Category{Name: "  Galletitas   dulces ", TaxID: 1},
			wantName: "Galletitas dulces",
		},
		{
			name:     "accents count as one character",
			category: model.Category{Name: "Té", TaxID: 1},
			wantName: "Té",
		},
		{
			name:     "max length",
			category: model.Category{Name: strings.Repeat("a", model.CategoryNameMaxLen), TaxID: 1},
			wantName: strings.Repeat("a", model.CategoryNameMaxLen),
		},
		{
			name:     "empty name",
			category: model.Category{Name: "   ", TaxID: 1},
			wantErr:  model.ErrEmptyCategoryName,
		},
		{
			name:     "name too short",
			category: model.Category{Name: "A", TaxID: 1},
			wantErr:  model.ErrCategoryNameTooShort,
		},
		{
			name:     "name too long",
			category: model.Category{Name: strings.Repeat("a", model.CategoryNameMaxLen+1), TaxID: 1},
			wantErr:  model.ErrCategoryNameTooLong,
		},
		{
			name:     "missing tax",
			category: model.Category{Name: "Limpieza"},
			wantErr:  model.ErrCategoryTaxRequired,
		},
		{
			name:     "negative tax",
			category: model.Category{Name: "Limpieza", TaxID: -1},
			wantErr:  model.ErrCategoryTaxRequired,
		},
		{
			name:     "parent zero is not 'no parent'",
			category: model.Category{Name: "Jabones", TaxID: 1, ParentID: new(int64(0))},
			wantErr:  model.ErrInvalidCategoryParent,
		},
		{
			name:     "negative parent",
			category: model.Category{Name: "Jabones", TaxID: 1, ParentID: new(int64(-5))},
			wantErr:  model.ErrInvalidCategoryParent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.category
			err := c.Validate()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && c.Name != tt.wantName {
				t.Fatalf("name: got %q, want %q", c.Name, tt.wantName)
			}
		})
	}
}

func TestCategoryValidateNil(t *testing.T) {
	var c *model.Category
	if err := c.Validate(); !errors.Is(err, model.ErrNilCategory) {
		t.Fatalf("error: got %v, want ErrNilCategory", err)
	}
}
