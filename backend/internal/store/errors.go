package store

import "errors"

var (
	// generic errors
	ErrNotFound  = errors.New("resource not found")

	// specific business errors
	ErrDuplicateTaxName = errors.New("tax name already exists")
)
