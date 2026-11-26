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

// formatQuantity muestra una cantidad (unidades o kg) sin ceros de más, con
// el separador decimal del idioma: "2,5" (es) o "2.5" (en). Sin separador de
// miles: se lee como se escribe en el formulario.
func formatQuantity(lang string, d decimal.Decimal) string {
	return formatPercent(lang, d)
}

// parseFormMoney lee un importe escrito a mano, como se escribe plata en el
// almacén: "15.000", "15000", "1.500,50", "$ 2.000" o "12,5". Vacío o mal
// escrito: false.
//
// A diferencia de una cantidad (parseFormDecimal), un importe nunca tiene 3
// decimales: un punto seguido de 3 dígitos es de miles ("15.000" son quince
// mil, no quince). Con punto y coma, el que va último es el decimal
// ("1.500,50" o "1,500.50").
func parseFormMoney(s string) (decimal.Decimal, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "$"))
	dot, comma := strings.LastIndex(s, "."), strings.LastIndex(s, ",")
	switch {
	case dot >= 0 && comma >= 0:
		thousands, dec := ".", ","
		if dot > comma {
			thousands, dec = ",", "."
		}
		s = strings.ReplaceAll(s, thousands, "")
		s = strings.Replace(s, dec, ".", 1)
	case comma >= 0:
		s = strings.Replace(s, ",", ".", 1)
	case dot >= 0 && (strings.Count(s, ".") > 1 || len(s)-dot-1 == 3):
		s = strings.ReplaceAll(s, ".", "")
	}
	if s == "" {
		return decimal.Zero, false
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, false
	}
	return d, true
}
