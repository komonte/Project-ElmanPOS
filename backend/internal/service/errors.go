package service

import "errors"

var (
	ErrInvalidID     = errors.New("invalid id")
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrInUse         = errors.New("in use by other records")

	ErrInvalidReference = errors.New("invalid reference")

	// ErrValidation envuelve lo que devuelve Validate() (nombre vacío, CUIT
	// inválido...). Así quien llama sabe que es un error del dato que mandó
	// sin conocer los errores de cada model, y errors.Is sigue encontrando
	// el error puntual (ej. model.ErrEmptyBrandName) adentro.
	ErrValidation = errors.New("validation failed")

	// ErrConflict: el pedido choca con cómo están los datos ahora (ej.
	// desactivar una categoría que tiene subcategorías activas).
	ErrConflict = errors.New("conflict")
)
