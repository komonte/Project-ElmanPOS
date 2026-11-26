package countcsv

import (
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func TestParse(t *testing.T) {
	// Como lo guarda Excel en español: BOM, ";", coma decimal, columnas en
	// otro orden, una que no se usa y una fila vacía.
	in := "\xef\xbb\xbfCantidad;Código;Descripción;Sector;Costo;Vencimiento\r\n" +
		"12;7790895001611;Soda;Góndola 3;1.300,50;\r\n" +
		";;;;;\r\n" +
		"2,345;0000009000001;Mortadela;;4200;31/03/2027\r\n" +
		"0; 779 0001 ;;Depósito;;2027-01-15\r\n"
	rows, problems, err := Parse(strings.NewReader(in))
	if err != nil || len(problems) != 0 {
		t.Fatalf("parse: %v, problems %v", err, problems)
	}
	if len(rows) != 3 {
		t.Fatalf("rows: got %d, want 3: %+v", len(rows), rows)
	}
	r := rows[0]
	if r.Line != 2 || r.Code != "7790895001611" || !r.Quantity.Equal(decimal.NewFromInt(12)) || r.Sector != "Góndola 3" ||
		r.Cost == nil || !r.Cost.Equal(decimal.RequireFromString("1300.50")) || r.Expiry != "" {
		t.Errorf("row 1: got %+v", r)
	}
	r = rows[1]
	if r.Line != 4 || !r.Quantity.Equal(decimal.RequireFromString("2.345")) || r.Sector != "" || r.Cost == nil ||
		r.Expiry != "2027-03-31" {
		t.Errorf("row 2: got %+v", r)
	}
	r = rows[2]
	if r.Line != 5 || r.Code != "7790001" || !r.Quantity.IsZero() || r.Cost != nil || r.Expiry != "2027-01-15" {
		t.Errorf("row 3: got %+v", r)
	}
}

// En inglés: "," como separador y punto decimal; solo las obligatorias.
func TestParseCommaSeparated(t *testing.T) {
	rows, problems, err := Parse(strings.NewReader("codigo,cantidad\n7790895001611,2.5\n\"000123\",\"10\"\n"))
	if err != nil || len(problems) != 0 || len(rows) != 2 {
		t.Fatalf("got %+v, %v, %v", rows, problems, err)
	}
	if !rows[0].Quantity.Equal(decimal.RequireFromString("2.5")) || rows[1].Code != "000123" {
		t.Errorf("got %+v", rows)
	}
}

// Una fila con problema no corta la lectura: las demás se leen.
func TestParseLineProblems(t *testing.T) {
	in := "codigo;cantidad;costo;vencimiento\n" +
		"7790895001611;doce;;\n" +
		";3;;\n" +
		"7,79089E+12;3;;\n" +
		"7790895001611;;;\n" +
		"7790895001611;3;caro;\n" +
		"7790895001611;3;;mañana\n" +
		"7790895001611;3;;\n"
	rows, problems, err := Parse(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Line != 8 {
		t.Errorf("rows: got %+v, want only line 8", rows)
	}
	want := []struct {
		line int
		err  error
	}{
		{2, ErrQuantityInvalid}, {3, ErrCodeMissing}, {4, ErrCodeScientific}, {5, ErrQuantityMissing},
		{6, ErrCostInvalid}, {7, ErrExpiryInvalid},
	}
	if len(problems) != len(want) {
		t.Fatalf("problems: got %v", problems)
	}
	for i, w := range want {
		if problems[i].Line != w.line || !errors.Is(problems[i], w.err) {
			t.Errorf("problem %d: got %v, want line %d %v", i, problems[i], w.line, w.err)
		}
	}
}

func TestParseFileErrors(t *testing.T) {
	if _, _, err := Parse(strings.NewReader(" \n\n")); !errors.Is(err, ErrEmptyFile) {
		t.Errorf("empty: got %v", err)
	}
	_, _, err := Parse(strings.NewReader("codigo;stock\n7790895001611;3\n"))
	var missing *MissingColumnError
	if !errors.As(err, &missing) || missing.Column != ColQuantity {
		t.Errorf("missing cantidad: got %v", err)
	}
	if _, _, err := Parse(strings.NewReader("producto;cantidad\nsoda;3\n")); !errors.As(err, &missing) || missing.Column != ColCode {
		t.Errorf("missing codigo: got %v", err)
	}
}

func TestParseNumber(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"2,5", "2.5", true}, {"2.5", "2.5", true}, {"1.234,5", "1234.5", true}, {"$ 1.500,00", "1500", true},
		{"1500", "1500", true}, {"-1", "-1", true}, {"doce", "", false}, {"", "", false},
	}
	for _, tt := range tests {
		got, ok := ParseNumber(tt.in)
		if ok != tt.ok || (ok && !got.Equal(decimal.RequireFromString(tt.want))) {
			t.Errorf("ParseNumber(%q) = %s, %v; want %s, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
