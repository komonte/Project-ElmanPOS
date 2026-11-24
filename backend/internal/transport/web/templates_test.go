package web

import (
	"bytes"
	"strings"
	"testing"
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
	}

	for _, lang := range []string{"en", "es"} {
		for page, want := range pages {
			var buf bytes.Buffer
			data := map[string]any{"Lang": lang, "Section": "", "TitleKey": ""}
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
