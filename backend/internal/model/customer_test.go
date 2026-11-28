package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

func TestCustomerValidate(t *testing.T) {
	c := &model.Customer{Name: "  juan   Pérez ", Phone: " 11 5555-0000 ", Notes: " vecino "}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Name != "juan Pérez" || c.Phone != "11 5555-0000" || c.Notes != "vecino" {
		t.Errorf("trimmed: %+v", c)
	}
	tests := []struct {
		name string
		c    *model.Customer
		want error
	}{
		{"nil", nil, model.ErrNilCustomer},
		{"empty", &model.Customer{Name: "   "}, model.ErrCustomerNameRequired},
		{"one letter", &model.Customer{Name: "J"}, model.ErrCustomerNameTooShort},
		{"long name", &model.Customer{Name: strings.Repeat("a", 121)}, model.ErrCustomerNameTooLong},
		{"long phone", &model.Customer{Name: "Juan", Phone: strings.Repeat("1", 41)}, model.ErrCustomerPhoneTooLong},
		{"long notes", &model.Customer{Name: "Juan", Notes: strings.Repeat("x", 301)}, model.ErrCustomerNotesTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.c.Validate(); !errors.Is(err, tt.want) {
				t.Errorf("err %v, want %v", err, tt.want)
			}
		})
	}
}
