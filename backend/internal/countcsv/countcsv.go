// Package countcsv lee la planilla de un conteo de stock (F-09): un CSV como
// el que guarda Excel ("Guardar como → CSV"). Solo entiende el formato: que
// las columnas estén y que los números y fechas se puedan leer. Si el código
// existe o la cantidad es válida lo decide el caso de uso, con las mismas
// reglas que una carga a mano.
//
// Formato:
//   - Primera fila con los nombres de las columnas, en cualquier orden:
//     codigo y cantidad (obligatorias), sector, costo y vencimiento
//     (opcionales). Mayúsculas y tildes no importan ("Código"); las columnas
//     que no conoce se ignoran.
//   - Separador ";" (Excel en español) o ",": se detecta en la primera fila.
//   - Números con coma decimal ("2,5"). Si el número tiene coma, los puntos
//     son de miles ("1.234,5"); si no tiene coma, el punto es decimal ("2.5").
//   - Vencimiento "2027-03-31" o "31/03/2027".
//   - Las filas vacías se saltean.
package countcsv

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Columnas que entiende.
const (
	ColCode     = "codigo"
	ColQuantity = "cantidad"
	ColSector   = "sector"
	ColCost     = "costo"
	ColExpiry   = "vencimiento"
)

var (
	// De la planilla entera.
	ErrEmptyFile = errors.New("the file is empty")

	// De una fila (van en LineError).
	ErrCodeMissing     = errors.New("the code is empty")
	ErrCodeScientific  = errors.New("the code is in scientific notation (7.79E+12): format the column as text in the spreadsheet")
	ErrQuantityMissing = errors.New("the quantity is empty")
	ErrQuantityInvalid = errors.New("the quantity is not a number")
	ErrCostInvalid     = errors.New("the cost is not a number")
	ErrExpiryInvalid   = errors.New("the expiry is not a date (2027-03-31 or 31/03/2027)")
)

// MissingColumnError: falta una columna obligatoria en la primera fila.
type MissingColumnError struct {
	Column string
}

func (e *MissingColumnError) Error() string {
	return fmt.Sprintf("the first row has no %q column", e.Column)
}

// Row es una fila leída. Line es su número en el archivo (la primera fila,
// la de los nombres, es la 1), para que el problema se encuentre en la
// planilla.
type Row struct {
	Line     int
	Code     string
	Quantity decimal.Decimal
	Sector   string           // vacío: el sector por defecto de la importación
	Cost     *decimal.Decimal // nil: sin costo
	Expiry   string           // "2027-03-31"; vacío: sin vencimiento
}

// LineError es el problema de una fila.
type LineError struct {
	Line int
	Err  error
}

func (e LineError) Error() string { return fmt.Sprintf("line %d: %v", e.Line, e.Err) }
func (e LineError) Unwrap() error { return e.Err }

// Parse lee la planilla. Devuelve las filas que se pudieron leer y los
// problemas de las demás (una fila con problema no corta la lectura). err es
// de la planilla entera: vacía, sin las columnas obligatorias o ilegible.
func Parse(r io.Reader) (rows []Row, problems []LineError, err error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, nil, err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // BOM que agrega Excel
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil, ErrEmptyFile
	}

	cr := csv.NewReader(bytes.NewReader(data))
	cr.Comma = delimiter(data)
	cr.FieldsPerRecord = -1 // Excel a veces deja filas más cortas
	cr.TrimLeadingSpace = true
	cr.LazyQuotes = true

	header, err := cr.Read()
	if err != nil {
		return nil, nil, err
	}
	cols := map[string]int{}
	for i, name := range header {
		cols[normalizeHeader(name)] = i
	}
	for _, required := range []string{ColCode, ColQuantity} {
		if _, ok := cols[required]; !ok {
			return nil, nil, &MissingColumnError{Column: required}
		}
	}

	for {
		record, err := cr.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		line, _ := cr.FieldPos(0)
		if err != nil {
			var perr *csv.ParseError
			if errors.As(err, &perr) {
				problems = append(problems, LineError{Line: perr.Line, Err: perr.Err})
				continue
			}
			return nil, nil, err
		}
		if blank(record) {
			continue
		}
		row, err := parseRow(record, cols)
		if err != nil {
			problems = append(problems, LineError{Line: line, Err: err})
			continue
		}
		row.Line = line
		rows = append(rows, row)
	}
	return rows, problems, nil
}

// parseRow lee una fila con las columnas que encontró en la primera.
func parseRow(record []string, cols map[string]int) (Row, error) {
	field := func(name string) string {
		i, ok := cols[name]
		if !ok || i >= len(record) {
			return ""
		}
		return strings.TrimSpace(record[i])
	}
	row := Row{Code: strings.Join(strings.Fields(field(ColCode)), ""), Sector: field(ColSector)}
	switch {
	case row.Code == "":
		return row, ErrCodeMissing
	case strings.Contains(strings.ToUpper(row.Code), "E+"):
		return row, ErrCodeScientific
	}

	raw := field(ColQuantity)
	if raw == "" {
		return row, ErrQuantityMissing
	}
	qty, ok := ParseNumber(raw)
	if !ok {
		return row, ErrQuantityInvalid
	}
	row.Quantity = qty

	if raw := field(ColCost); raw != "" {
		cost, ok := ParseNumber(raw)
		if !ok {
			return row, ErrCostInvalid
		}
		row.Cost = &cost
	}
	if raw := field(ColExpiry); raw != "" {
		expiry, ok := parseDate(raw)
		if !ok {
			return row, ErrExpiryInvalid
		}
		row.Expiry = expiry
	}
	return row, nil
}

// ParseNumber lee un número como lo escribe Excel en español o en inglés:
// "2,5", "1.234,5", "2.5", "$ 1.500,00". Con coma, los puntos son de miles.
func ParseNumber(s string) (decimal.Decimal, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "$"))
	s = strings.ReplaceAll(s, " ", "")
	if strings.Contains(s, ",") {
		s = strings.ReplaceAll(s, ".", "")
		s = strings.Replace(s, ",", ".", 1)
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, false
	}
	return d, true
}

// parseDate acepta "2027-03-31" y "31/03/2027" (también "31/3/2027") y
// devuelve "2027-03-31".
func parseDate(s string) (string, bool) {
	for _, layout := range []string{time.DateOnly, "2/1/2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Format(time.DateOnly), true
		}
	}
	return "", false
}

// delimiter elige ";" o "," según cuál aparece más en la primera fila.
func delimiter(data []byte) rune {
	first, _, _ := bufio.NewReader(bytes.NewReader(data)).ReadLine()
	if bytes.Count(first, []byte(";")) >= bytes.Count(first, []byte(",")) && bytes.Contains(first, []byte(";")) {
		return ';'
	}
	return ','
}

// normalizeHeader pasa un nombre de columna a la forma con que se busca:
// minúsculas, sin tildes ni espacios alrededor ("Código " → "codigo").
func normalizeHeader(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u").Replace(s)
}

func blank(record []string) bool {
	for _, f := range record {
		if strings.TrimSpace(f) != "" {
			return false
		}
	}
	return true
}
