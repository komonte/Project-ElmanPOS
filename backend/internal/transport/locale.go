package transport

import (
	"context"
	"net/http"

	"github.com/komonte/Project-ElmanPOS/backend/internal/i18n"
)

type contextKey string

const localeKey contextKey = "lang"

// LocaleMiddleware extracts the preferred language from the request and
// stores it in the context. It also sets the Content-Language response header.
func LocaleMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := i18n.LocaleFromRequest(r)
		ctx := context.WithValue(r.Context(), localeKey, lang)
		w.Header().Set("Content-Language", lang)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetLocale retrieves the language code from the request context.
// Returns "en" if not set.
func GetLocale(ctx context.Context) string {
	if lang, ok := ctx.Value(localeKey).(string); ok {
		return lang
	}
	return "en"
}

// T is a convenience function that translates a key using the locale from context.
func T(ctx context.Context, key string) string {
	return i18n.T(GetLocale(ctx), key)
}