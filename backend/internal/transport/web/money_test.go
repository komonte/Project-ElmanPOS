package web

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestFormatMoney(t *testing.T) {
	tests := []struct {
		lang, in, want string
	}{
		{"es", "1500.5", "$ 1.500,50"},
		{"es", "0.01", "$ 0,01"},
		{"es", "999", "$ 999,00"},
		{"es", "1000", "$ 1.000,00"},
		{"es", "1234567.89", "$ 1.234.567,89"},
		{"es", "-10", "-$ 10,00"},
		{"en", "1500.5", "$1,500.50"},
		{"en", "1234567.89", "$1,234,567.89"},
	}
	for _, tt := range tests {
		t.Run(tt.lang+" "+tt.in, func(t *testing.T) {
			if got := formatMoney(tt.lang, decimal.RequireFromString(tt.in)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
