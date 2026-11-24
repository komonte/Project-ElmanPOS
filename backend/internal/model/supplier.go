package model

import (
	"errors"
	"net/mail"
	"strings"
	"unicode/utf8"
)

const (
	SupplierNameMinLen = 2
	SupplierNameMaxLen = 160

	SupplierPhoneMinLen = 8
	SupplierPhoneMaxLen = 40

	SupplierEmailMaxLen = 120

	SupplierPaymentTermsMaxLen = 120
)

var (
	ErrNilSupplier = errors.New("supplier cannot be nil")

	ErrEmptySupplierName    = errors.New("supplier name is required")
	ErrSupplierNameTooShort = errors.New("supplier name must be at least 2 characters")
	ErrSupplierNameTooLong  = errors.New("supplier name cannot exceed 160 characters")

	ErrInvalidSupplierTaxID = errors.New("supplier tax id must be a valid CUIT (11 digits)")
	ErrInvalidSupplierPhone = errors.New("supplier phone must have at least 8 digits and only digits, spaces, '-', '(', ')' or a leading '+'")
	ErrInvalidSupplierEmail = errors.New("supplier email is invalid")

	ErrSupplierPaymentTermsTooLong = errors.New("supplier payment terms cannot exceed 120 characters")
)

type Supplier struct {
	ID           int64  `json:"id" db:"id"`
	Name         string `json:"name" db:"name"`
	TaxIDNumber  string `json:"tax_id_number" db:"tax_id_number"`
	Phone        string `json:"phone" db:"phone"`
	Email        string `json:"email" db:"email"`
	PaymentTerms string `json:"payment_terms" db:"payment_terms"`
	IsActive     bool   `json:"is_active" db:"is_active"`
	CreatedAt    string `json:"created_at" db:"created_at"`
	UpdatedAt    string `json:"updated_at" db:"updated_at"`
}

func (s *Supplier) Validate() error {
	if s == nil {
		return ErrNilSupplier
	}

	// Name
	s.Name = strings.Join(strings.Fields(s.Name), " ")
	if s.Name == "" {
		return ErrEmptySupplierName
	}

	nameLen := utf8.RuneCountInString(s.Name)
	if nameLen < SupplierNameMinLen {
		return ErrSupplierNameTooShort
	}
	if nameLen > SupplierNameMaxLen {
		return ErrSupplierNameTooLong
	}

	// TaxIdNumber (opcional): "20-12345678-6" -> "20123456786"
	s.TaxIDNumber = taxIDSeparators.Replace(s.TaxIDNumber)
	if s.TaxIDNumber != "" && !validCUIT(s.TaxIDNumber) {
		return ErrInvalidSupplierTaxID
	}

	// Phone (opcional): se guarda como lo escribió el usuario, solo con los
	// espacios ordenados: "  +54 (11)   4555-1234 " -> "+54 (11) 4555-1234"
	s.Phone = strings.Join(strings.Fields(s.Phone), " ")
	if s.Phone != "" && !validPhone(s.Phone) {
		return ErrInvalidSupplierPhone
	}

	// Email (opcional): " Ventas@Arcor.COM " -> "ventas@arcor.com"
	s.Email = strings.ToLower(strings.TrimSpace(s.Email))
	if s.Email != "" && !validEmail(s.Email) {
		return ErrInvalidSupplierEmail
	}

	// PaymentTerms (opcional, texto libre): "  30   días " -> "30 días"
	s.PaymentTerms = strings.Join(strings.Fields(s.PaymentTerms), " ")
	if utf8.RuneCountInString(s.PaymentTerms) > SupplierPaymentTermsMaxLen {
		return ErrSupplierPaymentTermsTooLong
	}

	return nil
}

// validPhone informa si s parece un teléfono: solo dígitos, espacios, '-',
// '(' y ')', con un '+' opcional al principio; entre SupplierPhoneMinLen
// dígitos y SupplierPhoneMaxLen caracteres en total.
func validPhone(s string) bool {
	digits := 0
	for i, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == ' ' || r == '-' || r == '(' || r == ')':
			// separadores permitidos
		case r == '+' && i == 0:
			// prefijo internacional, solo al principio
		default:
			return false
		}
	}

	// Todo es ASCII, así que len (bytes) == cantidad de caracteres.
	return digits >= SupplierPhoneMinLen && len(s) <= SupplierPhoneMaxLen
}

// validEmail informa si s es una dirección de mail simple (sin nombre ni
// comentarios) de hasta SupplierEmailMaxLen caracteres, con un dominio que
// tenga al menos un punto.
func validEmail(s string) bool {
	if utf8.RuneCountInString(s) > SupplierEmailMaxLen {
		return false
	}

	// ParseAddress acepta "Juan <juan@x.com>" y devuelve solo "juan@x.com":
	// si el resultado no es igual a la entrada, había algo más que un mail.
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Address != s {
		return false
	}

	// "ventas@arcor" es válido según el RFC, pero no sirve para un proveedor.
	_, domain, _ := strings.Cut(s, "@")
	return strings.Contains(domain, ".")
}

// taxIDSeparators borra los separadores que la gente tipea en un CUIT.
// Argumentos de a pares (viejo, nuevo): " " -> "", "-" -> "", "." -> "".
var taxIDSeparators = strings.NewReplacer(" ", "", "-", "", ".", "")

// cuitWeights pesos oficiales de ARCA para los primeros 10 dígitos del CUIT.
var cuitWeights = [10]int{5, 4, 3, 2, 7, 6, 5, 4, 3, 2}

// validCUIT informa si s es un CUIT válido: 11 dígitos ASCII con el dígito
// verificador correcto. Espera s ya normalizado (sin espacios, guiones ni puntos).
func validCUIT(s string) bool {
	// Todos dígitos ASCII. Se compara contra '0'..'9' y no con unicode.IsDigit,
	// que también acepta dígitos de otros alfabetos (ej. '٣').
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	if len(s) != 11 {
		return false
	}

	// Suma ponderada de los primeros 10 dígitos.
	// s[i] es un byte ('7' == 55); restarle '0' (48) da el valor numérico (7).
	sum := 0
	for i, w := range cuitWeights {
		sum += int(s[i]-'0') * w
	}

	dv := 11 - sum%11
	switch dv {
	case 11:
		dv = 0
	case 10:
		return false // ningún CUIT válido da 10
	}

	return dv == int(s[10]-'0')
}
