package model

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	CategoryNameMinLen = 2
	CategoryNameMaxLen = 120
)

var (
	ErrNilCategory = errors.New("category cannot be nil")

	ErrEmptyCategoryName    = errors.New("category name is required")
	ErrCategoryNameTooShort = errors.New("category name must be at least 2 characters")
	ErrCategoryNameTooLong  = errors.New("category name cannot exceed 120 characters")

	ErrCategoryTaxRequired   = errors.New("category tax is required")
	ErrInvalidCategoryParent = errors.New("category parent id must be positive")
)

// Category es un nodo del árbol de categorías (02 4.2). Una categoría raíz
// no tiene padre: ParentID es nil (NULL en la DB). Se usa un puntero porque
// un int64 no tiene un "vacío": 0 sería un id inválido, no "sin padre".
//
// Los productos toman el IVA de su propia categoría (TaxID), no del padre.
type Category struct {
	ID       int64  `json:"id" db:"id"`
	ParentID *int64 `json:"parent_id" db:"parent_id"`
	Name     string `json:"name" db:"name"`
	TaxID    int64  `json:"tax_id" db:"tax_id"`
	IsActive bool   `json:"is_active" db:"is_active"`

	// Solo lectura: el store los llena con un JOIN para mostrar la tabla
	// sin otra consulta por fila. Create y Update los ignoran.
	ParentName string `json:"parent_name,omitempty" db:"-"`
	TaxName    string `json:"tax_name,omitempty" db:"-"`

	CreatedAt string `json:"created_at" db:"created_at"`
	UpdatedAt string `json:"updated_at" db:"updated_at"`
}

// Validate normaliza y valida los datos que escribe el usuario. No revisa que
// el padre o el impuesto existan (eso lo hace la FK) ni los ciclos del árbol
// (necesitan recorrer la DB): esos los detecta el store.
func (c *Category) Validate() error {
	if c == nil {
		return ErrNilCategory
	}

	// Name: "  Galletitas   dulces " -> "Galletitas dulces".
	// No se pasa a mayúsculas: el índice único ya compara con LOWER(name).
	c.Name = strings.Join(strings.Fields(c.Name), " ")
	if c.Name == "" {
		return ErrEmptyCategoryName
	}

	nameLen := utf8.RuneCountInString(c.Name)
	if nameLen < CategoryNameMinLen {
		return ErrCategoryNameTooShort
	}
	if nameLen > CategoryNameMaxLen {
		return ErrCategoryNameTooLong
	}

	// TaxID es obligatorio (NOT NULL): 0 significa que no se eligió.
	if c.TaxID <= 0 {
		return ErrCategoryTaxRequired
	}

	// ParentID es opcional, pero si viene tiene que ser un id posible.
	// "Sin padre" es nil, no 0.
	if c.ParentID != nil && *c.ParentID <= 0 {
		return ErrInvalidCategoryParent
	}

	return nil
}
