package model

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// Clientes para vender fiado (2026-11-28, migración 0025). Decisiones:
//   - Solo el nombre es obligatorio; no hay límite de crédito.
//   - El nombre no se repite (sin mirar mayúsculas ni espacios de las
//     puntas): así la caja no abre una segunda cuenta para la misma persona.
//   - Se desactiva, no se borra: las ventas lo nombran.
//   - La caja (cajero o admin) da de alta un cliente con solo el nombre al
//     fiar; editar y desactivar es del admin (permiso customers).

const (
	CustomerNameMinLen  = 2
	CustomerNameMaxLen  = 120 // VARCHAR(120)
	CustomerPhoneMaxLen = 40  // VARCHAR(40)
	CustomerNotesMaxLen = 300 // VARCHAR(300)
)

var (
	ErrNilCustomer          = errors.New("customer cannot be nil")
	ErrCustomerNameRequired = errors.New("customer name is required")
	ErrCustomerNameTooShort = errors.New("customer name must have at least 2 characters")
	ErrCustomerNameTooLong  = errors.New("customer name cannot exceed 120 characters")
	ErrCustomerPhoneTooLong = errors.New("customer phone cannot exceed 40 characters")
	ErrCustomerNotesTooLong = errors.New("customer notes cannot exceed 300 characters")
)

// Customer es un cliente (cuenta corriente).
type Customer struct {
	ID       int64  `json:"id" db:"id"`
	Name     string `json:"name" db:"name"`
	Phone    string `json:"phone" db:"phone"` // vacío es NULL
	Notes    string `json:"notes" db:"notes"` // vacío es NULL
	IsActive bool   `json:"is_active" db:"is_active"`
	// CreatedBy es quién lo dio de alta (F-13).
	CreatedBy *int64 `json:"created_by" db:"created_by"`
	CreatedAt string `json:"created_at" db:"created_at"`
	UpdatedAt string `json:"updated_at" db:"updated_at"`
}

// Validate recorta y valida lo que se escribe: nombre (2 a 120), teléfono
// (hasta 40) y nota (hasta 300). Los espacios de más adentro del nombre se
// juntan ("Juan   Pérez" → "Juan Pérez").
func (c *Customer) Validate() error {
	if c == nil {
		return ErrNilCustomer
	}
	c.Name = strings.Join(strings.Fields(c.Name), " ")
	c.Phone = strings.TrimSpace(c.Phone)
	c.Notes = strings.TrimSpace(c.Notes)
	switch n := utf8.RuneCountInString(c.Name); {
	case n == 0:
		return ErrCustomerNameRequired
	case n < CustomerNameMinLen:
		return ErrCustomerNameTooShort
	case n > CustomerNameMaxLen:
		return ErrCustomerNameTooLong
	}
	if utf8.RuneCountInString(c.Phone) > CustomerPhoneMaxLen {
		return ErrCustomerPhoneTooLong
	}
	if utf8.RuneCountInString(c.Notes) > CustomerNotesMaxLen {
		return ErrCustomerNotesTooLong
	}
	return nil
}
