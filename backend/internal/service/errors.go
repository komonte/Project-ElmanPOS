package service

import (
	"errors"
)

var (
	ErrInvalidID = errors.New("invalid tax id")
	ErrTaxAlreadyExists  = errors.New("tax with this name already exists")
	ErrTaxNotFound       = errors.New("tax not found")
	ErrTaxRequired       = errors.New("tax data is required")
	ErrInvalidName       = errors.New("tax name is required")
	ErrInvalidNameMinLen = errors.New("tax name must have at least 2 characters")
	ErrInvalidNameMaxLen = errors.New("tax name must have less than 80 characters")
	ErrInvalidMinRate    = errors.New("tax rate cannot be negative")
	ErrInvalidMaxRate    = errors.New("tax rate cannot exceed 100%")
)
