package model_test

import (
	"errors"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

func TestNormalizeBarcode(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{name: "EAN-13 stays as is", in: "7790070991232", want: "7790070991232"},
		{name: "scanner whitespace is trimmed", in: " 7790070991232\n", want: "7790070991232"},
		{name: "EAN-8 is left-padded to 13", in: "40170725", want: "0000040170725"},
		{name: "UPC-A is left-padded to 13", in: "036000291452", want: "0036000291452"},
		{name: "short internal code is padded", in: "9000001", want: "0000009000001"},
		{name: "already padded stays the same", in: "0000040170725", want: "0000040170725"},
		{name: "14 digits are kept (DUN-14)", in: "17790070991239", want: "17790070991239"},
		{name: "empty", in: "   ", wantErr: model.ErrEmptyBarcode},
		{name: "letters", in: "ABC123", wantErr: model.ErrBarcodeNotDigits},
		{name: "inner space", in: "779 0070991232", wantErr: model.ErrBarcodeNotDigits},
		{name: "dash", in: "779-0070991232", wantErr: model.ErrBarcodeNotDigits},
		{name: "non-ASCII digit", in: "７７９", wantErr: model.ErrBarcodeNotDigits},
		{name: "15 digits", in: "123456789012345", wantErr: model.ErrBarcodeTooLong},
		{name: "all zeros", in: "0000", wantErr: model.ErrBarcodeAllZeros},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := model.NormalizeBarcode(tt.in)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err: got %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// Los casos de tipo usan códigos ya normalizados (13 o 14 dígitos), que es
// lo que recibe BarcodeType.
func TestBarcodeType(t *testing.T) {
	tests := []struct {
		name string
		code string
		want model.CodeType
	}{
		{name: "valid EAN-13", code: "7790070991232", want: model.CodeTypeEAN13},
		{name: "EAN-13 with wrong check digit", code: "7790070991234", want: model.CodeTypeInternal},
		{name: "UPC-A (12 digits) counts as EAN-13", code: "0712345678904", want: model.CodeTypeEAN13},
		// Igual que el EAN-8: un UPC-A que empieza con 0 queda con 11 dígitos al recortar.
		{name: "UPC-A starting with 0 is INTERNAL, like the SQL rule", code: "0036000291452", want: model.CodeTypeInternal},
		{name: "scale code (prefix 2) with valid check digit", code: "2012345000001", want: model.CodeTypeInternal},
		{name: "valid EAN-8", code: "0000040170725", want: model.CodeTypeEAN8},
		{name: "EAN-8 with wrong check digit", code: "0000040170726", want: model.CodeTypeInternal},
		// Igual que clean_catalog.sql: el 0 de adelante se recorta y quedan 7 dígitos.
		{name: "EAN-8 starting with 0 is INTERNAL, like the SQL rule", code: "0000001234565", want: model.CodeTypeInternal},
		{name: "short store code", code: "0000009000001", want: model.CodeTypeInternal},
		{name: "10 digits with valid check digit", code: "0001234567895", want: model.CodeTypeInternal},
		{name: "DUN-14 with valid check digit", code: "17790070991239", want: model.CodeTypeEAN13},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := model.BarcodeType(tt.code); got != tt.want {
				t.Errorf("BarcodeType(%q) = %s, want %s", tt.code, got, tt.want)
			}
		})
	}
}

func TestProductCodeValidate(t *testing.T) {
	t.Run("normalizes the code and overrides the type", func(t *testing.T) {
		c := model.ProductCode{Code: " 40170725 ", CodeType: model.CodeTypeInternal}
		if err := c.Validate(); err != nil {
			t.Fatalf("validate: %v", err)
		}
		if c.Code != "0000040170725" || c.CodeType != model.CodeTypeEAN8 {
			t.Errorf("got %q/%s, want 0000040170725/EAN8", c.Code, c.CodeType)
		}
	})
	t.Run("invalid code", func(t *testing.T) {
		c := model.ProductCode{Code: "12A"}
		if err := c.Validate(); !errors.Is(err, model.ErrBarcodeNotDigits) {
			t.Errorf("got %v, want ErrBarcodeNotDigits", err)
		}
	})
	t.Run("nil", func(t *testing.T) {
		var c *model.ProductCode
		if err := c.Validate(); !errors.Is(err, model.ErrNilProductCode) {
			t.Errorf("got %v, want ErrNilProductCode", err)
		}
	})
}
