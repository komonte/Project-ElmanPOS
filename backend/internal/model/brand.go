// Package model defines domain entities, data structures, and types for ElmanPOS.
package model

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	BrandNameMinLen = 2
	BrandNameMaxLen = 120
)

var (
	ErrNilBrand = errors.New("brand cannot be nil")

	ErrEmptyBrandName = errors.New("brand name is required")
	ErrBrandNameTooShort = errors.New("brand name must be at least 2 characters")
	ErrBrandNameTooLong  = errors.New("brand name cannot exceed 120 characters")
)

type Brand struct {
	ID int64 `json:"id" db:"id"`
	Name string `json:"name" db:"name"`
}


func (b *Brand) Validate() error {
	if b == nil {
		return ErrNilBrand
	}


	b.Name = strings.ToUpper(strings.TrimSpace(b.Name))
	if b.Name == "" {
		return ErrEmptyBrandName
	}

	nameLen := utf8.RuneCountInString(b.Name)
	if nameLen < BrandNameMinLen {
		return ErrBrandNameTooShort
	}
	if nameLen > BrandNameMaxLen {
		return ErrBrandNameTooLong
	}

	return nil
}
