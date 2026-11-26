package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

// Cada página tiene que salir con el layout completo, el menú y su propio
// contenido. Si dos páginas compartieran el bloque "content", una mostraría
// el contenido de otra: por eso se chequea el título de cada una.
func TestRenderPagesUseLayout(t *testing.T) {
	e := NewTemplateEngine()

	pages := map[string]string{
		"index.gohtml":     "/brands", // el panel no tiene tabla: alcanza con el menú
		"brands.gohtml":    `id="brands-table"`,
		"taxes.gohtml":     `id="taxes-table"`,
		"suppliers.gohtml":  `id="suppliers-table"`,
		"categories.gohtml": `id="categories-table"`,
		"products.gohtml":   `id="products-table"`,
	}

	for _, lang := range []string{"en", "es"} {
		for page, want := range pages {
			var buf bytes.Buffer
			data := map[string]any{"Lang": lang, "Section": "", "TitleKey": "", "Can": adminCan}
			if err := e.Render(&buf, lang, page, data); err != nil {
				t.Fatalf("%s/%s: %v", lang, page, err)
			}
			html := buf.String()
			for _, s := range []string{"<!DOCTYPE html>", `href="/suppliers"`, want, "</html>"} {
				if !strings.Contains(html, s) {
					t.Errorf("%s/%s: missing %q", lang, page, s)
				}
			}
		}
	}
}

func TestRenderPartialWithoutLayout(t *testing.T) {
	e := NewTemplateEngine()

	var buf bytes.Buffer
	if err := e.Render(&buf, "en", "brand_table.gohtml", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "<!DOCTYPE html>") {
		t.Error("partial rendered with layout")
	}
}

// Cada lista arranca con el cursor en el buscador: se busca (o se escanea,
// en productos) sin tocar el mouse.
func TestListPagesFocusSearch(t *testing.T) {
	e := NewTemplateEngine()
	for _, page := range []string{"brands.gohtml", "taxes.gohtml", "suppliers.gohtml", "categories.gohtml", "products.gohtml"} {
		var buf bytes.Buffer
		data := map[string]any{"Lang": "es", "Section": "", "TitleKey": ""}
		if err := e.Render(&buf, "es", page, data); err != nil {
			t.Fatalf("%s: %v", page, err)
		}
		if !strings.Contains(buf.String(), `id="search" type="search" name="search" autofocus`) {
			t.Errorf("%s: search has no autofocus", page)
		}
	}
}

// Las fechas vienen de la DB en UTC ("...T18:31:38Z"). En pantalla van en la
// hora del almacén (Argentina, UTC-3): un precio cargado a las 15:31 no puede
// decir 18:31.
func TestFormatTimeShowsStoreLocalTime(t *testing.T) {
	e := NewTemplateEngine()
	data := map[string]any{"Brands": []*model.Brand{
		{ID: 1, Name: "SANCOR", IsActive: true, CreatedAt: "2026-11-25T18:31:38.586087Z"},
		// Pasada la medianoche UTC sigue siendo el día anterior acá.
		{ID: 2, Name: "ARCOR", IsActive: true, CreatedAt: "2026-11-26T01:05:00Z"},
	}}
	var buf bytes.Buffer
	if err := e.Render(&buf, "es", "brand_table.gohtml", data); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{"2026-11-25 15:31:38", "2026-11-25 22:05:00"} {
		if !strings.Contains(html, want) {
			t.Errorf("missing local time %q", want)
		}
	}
	for _, utc := range []string{"18:31:38", "2026-11-26 01:05:00"} {
		if strings.Contains(html, utc) {
			t.Errorf("shows UTC time %q", utc)
		}
	}
}
