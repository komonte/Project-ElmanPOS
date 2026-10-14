package web

import (
	"embed"
	"html/template"
	"io"
	"log/slog"
	"time"

	"github.com/komonte/Project-ElmanPOS/backend/internal/i18n"
)

//go:embed templates/layouts/*.gohtml templates/partials/*.gohtml templates/pages/*.gohtml
var templateFS embed.FS

type TemplateEngine struct {
	templates map[string]*template.Template
}

func NewTemplateEngine() *TemplateEngine {
	langs := i18n.SupportedLanguages()
	templates := make(map[string]*template.Template, len(langs))

	files := []string{
		"templates/layouts/base.gohtml",
		"templates/partials/nav.gohtml",
		"templates/partials/brand_form.gohtml",
		"templates/partials/tax_form.gohtml",
		"templates/pages/index.gohtml",
		"templates/pages/brands.gohtml",
		"templates/pages/taxes.gohtml",
	}

	for _, lang := range langs {
		funcMap := template.FuncMap{
			"t": func(key string) string {
				return i18n.T(lang, key)
			},
			"formatTime": func(t time.Time) string {
				if t.IsZero() {
					return "-"
				}
				return t.Format("2006-01-02 15:04")
			},
		}

		tmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFS, files...)
		if err != nil {
			slog.Error("failed to parse templates", "lang", lang, "error", err)
			continue
		}

		templates[lang] = tmpl
	}

	return &TemplateEngine{templates: templates}
}

func (e *TemplateEngine) Render(w io.Writer, lang, name string, data map[string]any) error {
	tmpl, ok := e.templates[lang]
	if !ok {
		tmpl = e.templates["en"]
	}
	return tmpl.ExecuteTemplate(w, name, data)
}