package web

import (
	"embed"
	"errors"
	"html/template"
	"io"
	"log/slog"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/i18n"
)

//go:embed templates/layouts/*.gohtml templates/pages/*.gohtml templates/partials/*.gohtml
var templateFS embed.FS

const layoutFile = "templates/layouts/base.gohtml"

// pageFiles son las páginas completas. Cada una define el bloque "content"
// que base.gohtml dibuja dentro del layout.
var pageFiles = []string{
	"templates/pages/index.gohtml",
	"templates/pages/brands.gohtml",
	"templates/pages/taxes.gohtml",
	"templates/pages/suppliers.gohtml",
	"templates/pages/categories.gohtml",
	"templates/pages/products.gohtml",
}

// partialFiles son los fragmentos que htmx pide sueltos (tablas, formularios)
// y que las páginas también incluyen.
var partialFiles = []string{
	"templates/partials/nav.gohtml",
	"templates/partials/list_notice.gohtml",
	"templates/partials/brand_form.gohtml",
	"templates/partials/brand_table.gohtml",
	"templates/partials/tax_form.gohtml",
	"templates/partials/tax_table.gohtml",
	"templates/partials/supplier_form.gohtml",
	"templates/partials/supplier_table.gohtml",
	"templates/partials/category_form.gohtml",
	"templates/partials/category_table.gohtml",
	"templates/partials/product_form.gohtml",
	"templates/partials/product_table.gohtml",
}

type TemplateEngine struct {
	// pages[lang]["brands.gohtml"] = layout + parciales + esa página.
	// Cada página tiene su propio conjunto porque todas definen "content":
	// en un conjunto compartido la última pisaría a las demás.
	pages map[string]map[string]*template.Template
	// partials[lang] = solo los parciales, para las respuestas de htmx.
	partials map[string]*template.Template
}

func NewTemplateEngine() *TemplateEngine {
	langs := i18n.SupportedLanguages()
	e := &TemplateEngine{
		pages:    make(map[string]map[string]*template.Template, len(langs)),
		partials: make(map[string]*template.Template, len(langs)),
	}

	for _, lang := range langs {
		funcMap := template.FuncMap{
			"t": func(key string) string {
				return i18n.T(lang, key)
			},
			// dict arma un map para pasarle varios valores a un template
			// interno: (dict "Href" "/brands" "Count" 3).
			"dict": func(kv ...any) map[string]any {
				m := make(map[string]any, len(kv)/2)
				for i := 0; i+1 < len(kv); i += 2 {
					if k, ok := kv[i].(string); ok {
						m[k] = kv[i+1]
					}
				}
				return m
			},
			"formatTime": func(t string) string {
				if t == "" {
					return "-"
				}
				return strings.Replace(t[:19], "T", " ", 1)
			},
		}

		base, err := template.New("").Funcs(funcMap).ParseFS(templateFS, partialFiles...)
		if err != nil {
			slog.Error("ParseFS partials failed", "lang", lang, "error", err)
			continue
		}
		e.partials[lang] = base

		e.pages[lang] = make(map[string]*template.Template, len(pageFiles))
		for _, page := range pageFiles {
			set, err := template.Must(base.Clone()).ParseFS(templateFS, layoutFile, page)
			if err != nil {
				slog.Error("ParseFS page failed", "lang", lang, "page", page, "error", err)
				continue
			}
			name := page[strings.LastIndex(page, "/")+1:]
			e.pages[lang][name] = set
		}
	}

	return e
}

// Render dibuja una página completa (con layout) si name es una página,
// o el parcial suelto si no.
func (e *TemplateEngine) Render(w io.Writer, lang, name string, data map[string]any) error {
	if _, ok := e.partials[lang]; !ok {
		lang = "en"
	}
	if page, ok := e.pages[lang][name]; ok {
		return page.ExecuteTemplate(w, "base.gohtml", data)
	}
	partials, ok := e.partials[lang]
	if !ok {
		return errors.New("no templates for lang: " + lang)
	}
	return partials.ExecuteTemplate(w, name, data)
}
