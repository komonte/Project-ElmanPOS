package transport

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

// Permisos (F-13, PR D): cada ruta pide un permiso (model.Permission) y
// Require lo revisa del lado del server, después de AuthMiddleware. El menú
// y los botones también preguntan, pero esconder no alcanza: quien escribe
// la URL a mano llega acá.
//
// Sin permiso (403):
//   - la API JSON responde {"error": "forbidden"};
//   - htmx recibe el mensaje en texto (el aviso de la pantalla lo muestra);
//   - una página llama a Guard.Page (la página 403 de web).
// Queda en el log del server quién quiso entrar y a dónde.

// Guard revisa los permisos de cada ruta.
type Guard struct {
	// Page dibuja la página "no tenés permiso". nil: el mensaje en texto.
	Page func(w http.ResponseWriter, r *http.Request)
}

// Require deja pasar solo a un usuario con el permiso p.
func (g Guard) Require(p model.Permission, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := CurrentUser(r.Context())
		if u == nil {
			// Sin AuthMiddleware adelante, o una ruta pública con permiso:
			// mejor mandar a entrar que dejar pasar.
			notLoggedIn(w, r)
			return
		}
		if u.Can(p) {
			next.ServeHTTP(w, r)
			return
		}
		slog.Warn("forbidden", "username", u.Username, "role", u.Role, "permission", p,
			"method", r.Method, "path", r.URL.Path)
		g.forbidden(w, r)
	})
}

func (g Guard) forbidden(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/"):
		errorJSON(w, http.StatusForbidden, "forbidden")
	case r.Header.Get("HX-Request") == "true" || g.Page == nil:
		http.Error(w, T(r.Context(), "error.forbidden"), http.StatusForbidden)
	default:
		g.Page(w, r)
	}
}
