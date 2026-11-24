package store

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound = errors.New("resource not found")
	ErrInUse    = errors.New("resource in use by other records")

	// ErrInvalidReference: al crear o editar, una FK apunta a una fila que no
	// existe (ej. una categoría con un tax_id inexistente). Postgres usa el
	// mismo código (23503) para esto y para borrar algo en uso: el store
	// decide cuál es según la operación (Create/Update vs Delete).
	ErrInvalidReference = errors.New("referenced resource does not exist")

	// ErrDuplicate es la base de todos los errores de "ya existe".
	// Cada columna única tiene su propio error que la envuelve, así el mensaje
	// dice qué se repitió, pero el service solo pregunta por ErrDuplicate:
	// errors.Is(ErrDuplicateName, ErrDuplicate) == true.
	ErrDuplicate      = errors.New("already exists")
	ErrDuplicateName  = fmt.Errorf("name %w", ErrDuplicate)
	ErrDuplicateTaxID = fmt.Errorf("tax id %w", ErrDuplicate)

	// Árbol de categorías (02 4.2).
	ErrCategoryCycle             = errors.New("category cannot be moved under itself or one of its subcategories")
	ErrCategoryHasActiveChildren = errors.New("category has active subcategories")
	ErrCategoryParentInactive    = errors.New("category parent is inactive")
)
