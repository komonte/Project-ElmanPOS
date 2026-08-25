package store

import "errors"

var (
	// generic errors
	ErrNotFound = errors.New( "resource not found")
	ErrInvalidID         = errors.New("invalid tax id")

	// specific business errors
	ErrDuplicateTaxName = errors.New("tax name already exists")
)
