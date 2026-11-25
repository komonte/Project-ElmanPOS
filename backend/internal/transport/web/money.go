package web

import (
	"strings"

	"github.com/shopspring/decimal"
)

// formatMoney muestra un importe con 2 decimales y separador de miles, como
// se escribe en cada idioma: "$ 1.500,50" (es) o "$1,500.50" (en). Un
// negativo lleva el signo adelante: "-$ 10,00".
func formatMoney(lang string, d decimal.Decimal) string {
	sign := ""
	if d.IsNegative() {
		sign = "-"
		d = d.Neg()
	}
	whole, cents, _ := strings.Cut(d.StringFixed(2), ".")

	thousands, decimalSep, symbol := ",", ".", "$"
	if lang == "es" {
		thousands, decimalSep, symbol = ".", ",", "$ "
	}

	// Los miles se separan de a 3 dígitos, desde la derecha.
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteString(thousands)
		}
		b.WriteRune(r)
	}
	return sign + symbol + b.String() + decimalSep + cents
}

// formatPercent muestra un porcentaje sin ceros de más, con el separador
// decimal del idioma: "37,5" (es) o "37.5" (en). El "%" lo pone el template.
func formatPercent(lang string, d decimal.Decimal) string {
	s := d.String()
	if lang == "es" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}
