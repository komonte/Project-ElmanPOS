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

//go:embed templates/pages/*.gohtml templates/partials/*.gohtml
var templateFS embed.FS

type TemplateEngine struct {
	templates map[string]*template.Template
}

func NewTemplateEngine() *TemplateEngine {
	langs := i18n.SupportedLanguages()
	templates := make(map[string]*template.Template, len(langs))

	fileNames := []string{
		"templates/pages/index.gohtml",
		"templates/pages/brands.gohtml",
		"templates/pages/taxes.gohtml",
		"templates/partials/brand_form.gohtml",
		"templates/partials/tax_form.gohtml",
	}

	for _, lang := range langs {
		funcMap := template.FuncMap{
			"t": func(key string) string {
				return i18n.T(lang, key)
			},
			"formatTime": func(t string) string {
				if t == "" {
					return "-"
				}
				return strings.Replace(t[:19], "T", " ", 1)
			},
		}

		tmpl := template.New("").Funcs(funcMap)
		result, err := tmpl.ParseFS(templateFS, fileNames...)
		if err != nil {
			slog.Error("ParseFS failed", "error", err)
			continue
		}
		tmpl = result

		templates[lang] = tmpl
	}

	return &TemplateEngine{templates: templates}
}

func (e *TemplateEngine) Render(w io.Writer, lang, name string, data map[string]any) error {
	tmpl, ok := e.templates[lang]
	if !ok {
		tmpl, ok = e.templates["en"]
	}
	if !ok {
		return errors.New("no template engine for lang: " + lang)
	}
	return tmpl.ExecuteTemplate(w, name, data)
}