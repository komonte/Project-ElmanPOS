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

func TestFormatPercent(t *testing.T) {
	tests := []struct {
		lang, in, want string
	}{
		{"es", "30", "30"},
		{"es", "30.00", "30"},
		{"es", "37.5", "37,5"},
		{"es", "12.25", "12,25"},
		{"en", "37.5", "37.5"},
	}
	for _, tt := range tests {
		t.Run(tt.lang+" "+tt.in, func(t *testing.T) {
			if got := formatPercent(tt.lang, decimal.RequireFromString(tt.in)); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
