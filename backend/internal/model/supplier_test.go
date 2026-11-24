package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

func TestSupplierValidate(t *testing.T) {
	tests := []struct {
		name     string
		supplier model.Supplier
		wantErr  error
		want     model.Supplier // campos esperados después de normalizar
	}{
		// Nombre
		{
			name:     "only name",
			supplier: model.Supplier{Name: "Arcor"},
			want:     model.Supplier{Name: "Arcor"},
		},
		{
			name:     "name spaces are tidied",
			supplier: model.Supplier{Name: "  Distribuidora   del  Sur "},
			want:     model.Supplier{Name: "Distribuidora del Sur"},
		},
		{name: "empty name", supplier: model.Supplier{Name: "  "}, wantErr: model.ErrEmptySupplierName},
		{name: "name too short", supplier: model.Supplier{Name: "A"}, wantErr: model.ErrSupplierNameTooShort},
		{
			name:     "name max length",
			supplier: model.Supplier{Name: strings.Repeat("ñ", model.SupplierNameMaxLen)},
			want:     model.Supplier{Name: strings.Repeat("ñ", model.SupplierNameMaxLen)},
		},
		{
			name:     "name too long",
			supplier: model.Supplier{Name: strings.Repeat("a", model.SupplierNameMaxLen+1)},
			wantErr:  model.ErrSupplierNameTooLong,
		},

		// CUIT: se guarda solo con dígitos.
		{
			name:     "CUIT with dashes",
			supplier: model.Supplier{Name: "Arcor", TaxIDNumber: "20-12345678-6"},
			want:     model.Supplier{Name: "Arcor", TaxIDNumber: "20123456786"},
		},
		{
			name:     "CUIT with spaces and dots",
			supplier: model.Supplier{Name: "Arcor", TaxIDNumber: " 30.50279317 5 "},
			want:     model.Supplier{Name: "Arcor", TaxIDNumber: "30502793175"},
		},
		{
			name:     "CUIT with check digit 0 (11 - 0)",
			supplier: model.Supplier{Name: "Arcor", TaxIDNumber: "20000000060"},
			want:     model.Supplier{Name: "Arcor", TaxIDNumber: "20000000060"},
		},
		{name: "CUIT wrong check digit", supplier: model.Supplier{Name: "Arcor", TaxIDNumber: "20-12345678-7"}, wantErr: model.ErrInvalidSupplierTaxID},
		{name: "CUIT whose check digit would be 10", supplier: model.Supplier{Name: "Arcor", TaxIDNumber: "20000000010"}, wantErr: model.ErrInvalidSupplierTaxID},
		{name: "CUIT too short", supplier: model.Supplier{Name: "Arcor", TaxIDNumber: "2012345678"}, wantErr: model.ErrInvalidSupplierTaxID},
		{name: "CUIT too long", supplier: model.Supplier{Name: "Arcor", TaxIDNumber: "201234567860"}, wantErr: model.ErrInvalidSupplierTaxID},
		{name: "CUIT with a letter", supplier: model.Supplier{Name: "Arcor", TaxIDNumber: "20-1234567A-6"}, wantErr: model.ErrInvalidSupplierTaxID},
		{name: "CUIT with non-ASCII digits", supplier: model.Supplier{Name: "Arcor", TaxIDNumber: "٢0123456786"}, wantErr: model.ErrInvalidSupplierTaxID},

		// Teléfono: se guarda como lo escribieron, con los espacios ordenados.
		{
			name:     "phone with prefix and spaces",
			supplier: model.Supplier{Name: "Arcor", Phone: "  +54 (11)   4555-1234 "},
			want:     model.Supplier{Name: "Arcor", Phone: "+54 (11) 4555-1234"},
		},
		{
			name:     "phone with exactly 8 digits",
			supplier: model.Supplier{Name: "Arcor", Phone: "4555-1234"},
			want:     model.Supplier{Name: "Arcor", Phone: "4555-1234"},
		},
		{name: "phone with 7 digits", supplier: model.Supplier{Name: "Arcor", Phone: "455-1234"}, wantErr: model.ErrInvalidSupplierPhone},
		{name: "phone with + in the middle", supplier: model.Supplier{Name: "Arcor", Phone: "11 +4555-1234"}, wantErr: model.ErrInvalidSupplierPhone},
		{name: "phone with letters", supplier: model.Supplier{Name: "Arcor", Phone: "4555-1234 int 2"}, wantErr: model.ErrInvalidSupplierPhone},
		{
			name:     "phone too long",
			supplier: model.Supplier{Name: "Arcor", Phone: strings.Repeat("1-", 20) + "1"},
			wantErr:  model.ErrInvalidSupplierPhone,
		},

		// Email: en minúscula y sin espacios.
		{
			name:     "email is lowercased",
			supplier: model.Supplier{Name: "Arcor", Email: " Ventas@Arcor.COM "},
			want:     model.Supplier{Name: "Arcor", Email: "ventas@arcor.com"},
		},
		{name: "email without dot in domain", supplier: model.Supplier{Name: "Arcor", Email: "ventas@arcor"}, wantErr: model.ErrInvalidSupplierEmail},
		{name: "email with display name", supplier: model.Supplier{Name: "Arcor", Email: "Juan <juan@arcor.com>"}, wantErr: model.ErrInvalidSupplierEmail},
		{name: "email without @", supplier: model.Supplier{Name: "Arcor", Email: "ventas.arcor.com"}, wantErr: model.ErrInvalidSupplierEmail},
		{
			name:     "email too long",
			supplier: model.Supplier{Name: "Arcor", Email: strings.Repeat("a", model.SupplierEmailMaxLen) + "@arcor.com"},
			wantErr:  model.ErrInvalidSupplierEmail,
		},

		// Condiciones de pago: texto libre con los espacios ordenados.
		{
			name:     "payment terms spaces are tidied",
			supplier: model.Supplier{Name: "Arcor", PaymentTerms: "  30   días "},
			want:     model.Supplier{Name: "Arcor", PaymentTerms: "30 días"},
		},
		{
			name:     "payment terms too long",
			supplier: model.Supplier{Name: "Arcor", PaymentTerms: strings.Repeat("a", model.SupplierPaymentTermsMaxLen+1)},
			wantErr:  model.ErrSupplierPaymentTermsTooLong,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.supplier
			err := s.Validate()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && s != tt.want {
				t.Fatalf("got  %+v\nwant %+v", s, tt.want)
			}
		})
	}
}

func TestSupplierValidateNil(t *testing.T) {
	var s *model.Supplier
	if err := s.Validate(); !errors.Is(err, model.ErrNilSupplier) {
		t.Fatalf("error: got %v, want ErrNilSupplier", err)
	}
}
