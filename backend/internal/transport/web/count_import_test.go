package web

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/countcsv"
	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/stockcounts"
)

func TestImportErrMsg(t *testing.T) {
	h := &WebHandler{}
	// El idioma lo pone el middleware, como en la app.
	msg := func(err error) (got string) {
		r := httptest.NewRequest("POST", "/counts/1/import", nil)
		r.Header.Set("Accept-Language", "es")
		transport.LocaleMiddleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			got = h.importErrMsg(r, err)
		})).ServeHTTP(httptest.NewRecorder(), r)
		return got
	}
	_, corrupt := base64.StdEncoding.DecodeString("no es base64!")
	tests := []struct {
		err  error
		want string
	}{
		{&countcsv.MissingColumnError{Column: "cantidad"}, "La primera fila no tiene la columna «cantidad»."},
		{errImportTooLarge, "La planilla pesa más de 5 MB."},
		{corrupt, "La fila está mal armada (comillas sin cerrar)."},
		{countcsv.ErrCodeScientific, "Excel convirtió el código"},
		{fmt.Errorf("line 3: %w", stockcounts.ErrCodeNotFound), "Ningún producto tiene ese código"},
		{stockcounts.ErrImportSectorRequired, "Escribí en qué sector"},
	}
	for _, tt := range tests {
		if got := msg(tt.err); !strings.Contains(got, tt.want) {
			t.Errorf("%v: got %q, want %q", tt.err, got, tt.want)
		}
	}
}

func TestRenderCountImport(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, data map[string]any) string {
		t.Helper()
		data["Section"], data["TitleKey"] = "counts", "count.import_title"
		data["Count"] = &model.StockCount{ID: 5, Status: model.CountDraft}
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", "count_import.gohtml", data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	t.Run("form", func(t *testing.T) {
		html := render(t, map[string]any{"Sectors": []string{"Depósito"}, "Sector": "Depósito",
			"Error": "La primera fila no tiene la columna «cantidad»."})
		for _, s := range []string{`action="/counts/5/import" enctype="multipart/form-data"`,
			`type="file" name="file" accept=".csv,text/csv" required`, `name="sector" list="import-sectors"`,
			`value="Depósito"`, `<option value="Depósito"></option>`, "codigo;cantidad;sector;costo;vencimiento",
			`role="alert"`, "columna «cantidad»", `href="/counts"`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if strings.Contains(html, "/import/apply") {
			t.Error("the form must not offer to load before the preview")
		}
	})

	t.Run("preview", func(t *testing.T) {
		cost := decimal.RequireFromString("1300.5")
		html := render(t, map[string]any{"Sector": "Depósito", "Preview": map[string]any{
			"Lines": []stockcounts.ImportLine{
				{Row: countcsv.Row{Line: 2, Code: "7790895001611", Quantity: decimal.RequireFromString("12"), Sector: "Góndola 3", Cost: &cost},
					ProductName: "SODA COLA 1.5L"},
				{Row: countcsv.Row{Line: 4, Code: "000003", Quantity: decimal.RequireFromString("2.345"), Sector: "Depósito",
					Expiry: "2027-03-31"}, ProductName: "MORTADELA"},
			},
			"LineCount": 202, "Hidden": 200,
			"Problems": []importProblemRow{{Line: 3, Code: "779999", Message: "Ningún producto tiene ese código de barras o SKU."}},
			"FileData": "Y29kaWdvO2NhbnRpZGFk",
		}})
		for _, s := range []string{"202 filas entran y 1 tienen problemas", "Filas con problemas", ">779999<",
			"Ningún producto tiene ese código", "SODA COLA 1.5L", "Góndola 3", "$ 1.300,50", "2,345", "2027-03-31",
			"… y 200 filas más.", `action="/counts/5/import/apply"`, `name="file_data" value="Y29kaWdvO2NhbnRpZGFk"`,
			`name="sector" value="Depósito"`, "Cargar 202 filas", `href="/counts/5/import"`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if strings.Contains(html, `type="file"`) {
			t.Error("the preview must not show the upload form")
		}
	})

	t.Run("nothing goes in: no load button", func(t *testing.T) {
		html := render(t, map[string]any{"Sector": "A", "Preview": map[string]any{"LineCount": 0,
			"Problems": []importProblemRow{{Line: 2, Message: "Falta el código."}}, "FileData": "eA=="}})
		if strings.Contains(html, "/import/apply") || !strings.Contains(html, "0 filas entran y 1 tienen problemas") {
			t.Errorf("got %s", html)
		}
	})
}

func TestRenderCountsImportedNotice(t *testing.T) {
	e := NewTemplateEngine()
	for _, n := range []int{0, 12} {
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", "counts.gohtml", map[string]any{"Section": "counts", "TitleKey": "count.title",
			"Imported": n, "HasImported": true}); err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("Se cargaron %d filas de la planilla", n); !strings.Contains(buf.String(), want) {
			t.Errorf("imported %d: missing %q", n, want)
		}
	}
	var buf bytes.Buffer
	if err := e.Render(&buf, "es", "count_sheet.gohtml", map[string]any{"Count": &model.StockCount{ID: 5},
		"Form": countEntryForm{}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `href="/counts/5/import"`) {
		t.Error("the sheet must link to the import")
	}
}
