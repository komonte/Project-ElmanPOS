package model

import (
	"errors"
	"strings"
)

// CodeType es el tipo de un código de barras (02 §3, enum product_code_type).
// No lo elige el usuario: sale del código mismo (ver BarcodeType).
type CodeType string

const (
	CodeTypeEAN13    CodeType = "EAN13"
	CodeTypeEAN8     CodeType = "EAN8"
	CodeTypeInternal CodeType = "INTERNAL" // del comercio, de balanza o con verificador incorrecto
)

const (
	// BarcodeWidth: los códigos se guardan con ceros a la izquierda hasta 13
	// dígitos, como vienen en el catálogo. Un EAN-8 "40170725" queda
	// "0000040170725".
	BarcodeWidth = 13
	// BarcodeMaxLen: hasta 14 dígitos (cajas DUN-14). Es el VARCHAR(14) de la DB.
	BarcodeMaxLen = 14
)

var (
	ErrNilProductCode   = errors.New("product code cannot be nil")
	ErrEmptyBarcode     = errors.New("barcode is required")
	ErrBarcodeNotDigits = errors.New("barcode can only contain digits")
	ErrBarcodeTooLong   = errors.New("barcode cannot exceed 14 digits")
	ErrBarcodeAllZeros  = errors.New("barcode cannot be all zeros")
)

// ProductCode es un código de barras de un producto (02 4.5). Un producto
// puede tener varios (cambio de envase, EAN-13 y EAN-8); un código es de un
// solo producto (UNIQUE en la DB).
type ProductCode struct {
	ID        int64    `json:"id" db:"id"`
	ProductID int64    `json:"product_id" db:"product_id"`
	CodeType  CodeType `json:"code_type" db:"code_type"`
	Code      string   `json:"code" db:"code"`
	CreatedAt string   `json:"created_at" db:"created_at"`
}

// Validate normaliza el código y calcula su tipo: el CodeType que venga se
// pisa. No revisa ProductID: al dar de alta producto y código juntos, el
// producto todavía no tiene id cuando se valida.
func (c *ProductCode) Validate() error {
	if c == nil {
		return ErrNilProductCode
	}
	code, err := NormalizeBarcode(c.Code)
	if err != nil {
		return err
	}
	c.Code = code
	c.CodeType = BarcodeType(code)
	return nil
}

// NormalizeBarcode deja un código como se guarda: solo dígitos, con ceros a
// la izquierda hasta 13. Lo que lee el escáner pasa por acá antes de buscar,
// así "40170725" y "0000040170725" son el mismo código.
func NormalizeBarcode(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ErrEmptyBarcode
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return "", ErrBarcodeNotDigits
		}
	}
	if len(s) > BarcodeMaxLen {
		return "", ErrBarcodeTooLong
	}
	if strings.Trim(s, "0") == "" {
		return "", ErrBarcodeAllZeros
	}
	if len(s) < BarcodeWidth {
		s = strings.Repeat("0", BarcodeWidth-len(s)) + s
	}
	return s, nil
}

// BarcodeType calcula el tipo de un código ya normalizado. Es la misma regla
// que db/import/clean_catalog.sql, así el catálogo importado y la app dan lo
// mismo:
//   - EAN8: sin los ceros de adelante quedan 8 dígitos y el verificador da.
//   - EAN13: sin los ceros quedan 12 o más dígitos (UPC-A de 12 incluido), el
//     verificador da y no empieza con 2 (prefijo de balanza y de uso interno).
//   - INTERNAL: todo lo demás.
//
// Como en el SQL, un EAN-8 que empieza con 0 pierde ese cero al recortar y
// queda INTERNAL. Hoy el tipo es solo una etiqueta: nada depende de él.
func BarcodeType(code string) CodeType {
	digits := strings.TrimLeft(code, "0")
	switch {
	case len(digits) == 8 && gtinCheckDigitOK(digits):
		return CodeTypeEAN8
	case len(digits) >= 12 && gtinCheckDigitOK(code) && digits[0] != '2':
		return CodeTypeEAN13
	default:
		return CodeTypeInternal
	}
}

// gtinCheckDigitOK revisa el dígito verificador GS1 (sirve para EAN-8, UPC-A,
// EAN-13 y DUN-14). Desde la derecha, sin contar el verificador, los dígitos
// pesan 3, 1, 3, 1...; el verificador es lo que falta para llegar a la
// siguiente decena. Los ceros de adelante no cambian la cuenta.
func gtinCheckDigitOK(c string) bool {
	if len(c) < 8 || len(c) > BarcodeMaxLen {
		return false
	}
	sum := 0
	for i := 0; i < len(c)-1; i++ {
		d := int(c[i]) - '0'
		if d < 0 || d > 9 {
			return false
		}
		if (len(c)-1-i)%2 == 1 {
			d *= 3
		}
		sum += d
	}
	check := int(c[len(c)-1]) - '0'
	return check >= 0 && check <= 9 && (10-sum%10)%10 == check
}
