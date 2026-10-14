package i18n

import (
	"embed"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
)

//go:embed en.json es.json
var translationFiles embed.FS

var (
	translations map[string]map[string]string
	once         sync.Once
)

func loadTranslations() {
	langs := []string{"en", "es"}
	translations = make(map[string]map[string]string, len(langs))

	for _, lang := range langs {
		data, err := translationFiles.ReadFile(lang + ".json")
		if err != nil {
			slog.Error("failed to read translation file", "lang", lang, "error", err)
			translations[lang] = make(map[string]string)
			continue
		}
		var entries map[string]string
		if err := json.Unmarshal(data, &entries); err != nil {
			slog.Error("failed to parse translation file", "lang", lang, "error", err)
			translations[lang] = make(map[string]string)
			continue
		}
		translations[lang] = entries
	}
}

// T returns the translated string for the given key in the given language.
// Falls back to English if the key is missing in the requested language.
// Falls back to the key itself if missing in English.
func T(lang, key string) string {
	once.Do(loadTranslations)

	if lang != "en" {
		if m, ok := translations[lang]; ok {
			if v, ok := m[key]; ok {
				return v
			}
		}
	}

	if m, ok := translations["en"]; ok {
		if v, ok := m[key]; ok {
			return v
		}
	}

	return key
}

// SupportedLanguages returns the list of available language codes.
func SupportedLanguages() []string {
	once.Do(loadTranslations)
	langs := make([]string, 0, len(translations))
	for lang := range translations {
		langs = append(langs, lang)
	}
	return langs
}

// LocaleFromRequest extracts the preferred language from the request.
// Priority: cookie "lang" → Accept-Language header → "en"
func LocaleFromRequest(r *http.Request) string {
	if c, err := r.Cookie("lang"); err == nil {
		val := strings.TrimSpace(c.Value)
		if val == "es" || val == "en" {
			return val
		}
	}

	accept := r.Header.Get("Accept-Language")
	if accept != "" {
		lang := parseAcceptLanguage(accept)
		if lang != "" {
			return lang
		}
	}

	return "en"
}

func parseAcceptLanguage(header string) string {
	// Accept-Language: es-AR,es;q=0.9,en;q=0.8
	parts := strings.Split(header, ",")
	for _, part := range parts {
		lang := strings.TrimSpace(part)
		if idx := strings.IndexByte(lang, ';'); idx != -1 {
			lang = lang[:idx]
		}
		lang = strings.TrimSpace(lang)
		if idx := strings.IndexByte(lang, '-'); idx != -1 {
			lang = lang[:idx]
		}
		lang = strings.ToLower(strings.TrimSpace(lang))
		if lang == "es" || lang == "en" {
			return lang
		}
	}
	return ""
}