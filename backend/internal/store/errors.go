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
	ErrDuplicateSKU   = fmt.Errorf("sku %w", ErrDuplicate)
	ErrDuplicateCode  = fmt.Errorf("barcode %w", ErrDuplicate)

	// ErrConflict es la base de los errores de "choca con cómo están los
	// datos ahora" (ej. desactivar una categoría con hijas activas). Igual
	// que con ErrDuplicate, el service solo pregunta por ErrConflict y la
	// API responde 409 sin conocer cada regla.
	ErrConflict = errors.New("conflict")

	// Árbol de categorías (02 4.2).
	ErrCategoryCycle             = conflictError("category cannot be moved under itself or one of its subcategories")
	ErrCategoryHasActiveChildren = conflictError("category has active subcategories")
	ErrCategoryParentInactive    = conflictError("category parent is inactive")

	// Productos (F-01, BR-06): no se elige una marca, categoría o proveedor
	// inactivo. Al editar, sí se puede conservar el que ya tenía.
	ErrProductBrandInactive    = conflictError("product brand is inactive")
	ErrProductCategoryInactive = conflictError("product category is inactive")
	ErrProductSupplierInactive = conflictError("product preferred supplier is inactive")

	// F-01: con historial (movimientos, ventas, compras o lotes), cambiar
	// cómo se vende o se mide el producto falsearía lo ya registrado.
	ErrProductHasHistory = conflictError("product sale type, measure unit and batch control cannot change once it has stock history")

	// Locales (02 4.8). Una compra no dice a qué local entra: entra al único
	// activo. Sin local activo, o con más de uno, no se adivina.
	ErrNoActiveStore       = conflictError("there is no active store")
	ErrSeveralActiveStores = conflictError("there is more than one active store: stock cannot be assigned to one")

	// Conteos (F-09): uno en borrador por local; uno confirmado ya movió el
	// stock y no se toca más.
	ErrCountDraftExists = conflictError("there is already a stock count in progress")
	ErrCountNotDraft    = conflictError("stock count is already confirmed")

	// Saldos (02 4.13): el stock de un local nunca queda negativo.
	ErrNegativeStock = conflictError("stock cannot go below zero")
)

// conflictError es un error de regla que cumple errors.Is(err, ErrConflict).
// Con fmt.Errorf("...: %w", ErrConflict) pasaría lo mismo, pero el mensaje
// terminaría en ": conflict". Con el método Is, el mensaje queda limpio.
type conflictError string

func (e conflictError) Error() string        { return string(e) }
func (e conflictError) Is(target error) bool { return target == ErrConflict }
