package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

func TestBrandValidate(t *testing.T) {
	tests := []struct {
		name     string
		brand    model.Brand
		wantErr  error
		wantName string
	}{
		// Las marcas se guardan en mayúsculas, como vienen en el set de datos.
		{name: "uppercased and trimmed", brand: model.Brand{Name: "  bagley "}, wantName: "BAGLEY"},
		{name: "accents and ñ are uppercased", brand: model.Brand{Name: "La Serenísima ñandú"}, wantName: "LA SERENÍSIMA ÑANDÚ"},
		{name: "already uppercase", brand: model.Brand{Name: "COCA-COLA"}, wantName: "COCA-COLA"},
		{name: "max length", brand: model.Brand{Name: strings.Repeat("A", model.BrandNameMaxLen)}, wantName: strings.Repeat("A", model.BrandNameMaxLen)},
		{name: "empty name", brand: model.Brand{Name: "   "}, wantErr: model.ErrEmptyBrandName},
		{name: "name too short", brand: model.Brand{Name: "a"}, wantErr: model.ErrBrandNameTooShort},
		{name: "name too long", brand: model.Brand{Name: strings.Repeat("A", model.BrandNameMaxLen+1)}, wantErr: model.ErrBrandNameTooLong},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := tt.brand
			err := b.Validate()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && b.Name != tt.wantName {
				t.Fatalf("name: got %q, want %q", b.Name, tt.wantName)
			}
		})
	}
}

func TestBrandValidateNil(t *testing.T) {
	var b *model.Brand
	if err := b.Validate(); !errors.Is(err, model.ErrNilBrand) {
		t.Fatalf("error: got %v, want ErrNilBrand", err)
	}
}
