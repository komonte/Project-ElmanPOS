package store

import (
	"errors"
	"fmt"
)

var (
	ErrNotFound = errors.New("resource not found")
	ErrInUse    = errors.New("resource in use by other records")

	// ErrDuplicate es la base de todos los errores de "ya existe".
	// Cada columna única tiene su propio error que la envuelve, así el mensaje
	// dice qué se repitió, pero el service solo pregunta por ErrDuplicate:
	// errors.Is(ErrDuplicateName, ErrDuplicate) == true.
	ErrDuplicate      = errors.New("already exists")
	ErrDuplicateName  = fmt.Errorf("name %w", ErrDuplicate)
	ErrDuplicateTaxID = fmt.Errorf("tax id %w", ErrDuplicate)
)
