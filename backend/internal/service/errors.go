package service

import (
	"errors"
)

var (
	ErrInvalidID = errors.New("invalid tax id")
	ErrTaxAlreadyExists  = errors.New("tax with this name already exists")
	ErrTaxNotFound       = errors.New("tax not found")
)
