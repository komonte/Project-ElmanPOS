package service

import (
	"errors"
)

var (
	// generics
	ErrInvalidID        = errors.New("invalid id")

	// taxes
	ErrTaxAlreadyExists = errors.New("tax with this name already exists")
	ErrTaxNotFound      = errors.New("tax not found")

	// brands
	ErrBrandAlreadyExists = errors.New("brand with this name already exists")
	ErrBrandNotFound      = errors.New("brand not found")
)
