package transport

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/auth"
)

// Login (F-13): todo pide haber entrado, salvo la página de login, los
// archivos estáticos y el cambio de idioma (la página de login lo ofrece).
//
// Sin sesión válida:
//   - una página (GET) va a /login?next=<donde estaba>, para volver ahí;
//   - un pedido de htmx recibe HX-Redirect a /login (htmx no sigue un 303
//     con la página entera, la metería adentro de un pedazo de pantalla);
//   - la API JSON (/api/) responde 401;
//   - otro POST va a /login.
//
// El CSRF lo frena CrossOriginProtection (ver main): un POST de otra página
// no llega acá.

// SessionCookie es la cookie con el token de la sesión.
const SessionCookie = "elman_session"

const userKey contextKey = "user"

// Authenticator dice de quién es un token (auth.Service).
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (*model.UserSession, error)
}

// publicPath dice qué se ve sin haber entrado.
func publicPath(path string) bool {
	return path == "/login" || path == "/settings/language" || strings.HasPrefix(path, "/static/")
}

// AuthMiddleware deja pasar solo a quien tiene una sesión válida y pone su
// usuario en el contexto (CurrentUser). Una cookie que ya no vale se borra.
func AuthMiddleware(a Authenticator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		token := ""
		if c, err := r.Cookie(SessionCookie); err == nil {
			token = c.Value
		}
		sess, err := a.Authenticate(r.Context(), token)
		switch {
		case err == nil:
			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), sess.User)))
			return
		case !errors.Is(err, auth.ErrNotAuthenticated):
			// La base no contesta: mandar a entrar no arreglaría nada.
			slog.Error("cannot check the session", "error", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if token != "" {
			ClearSessionCookie(w, false)
		}
		notLoggedIn(w, r)
	})
}

// notLoggedIn manda a entrar, según quién pregunta.
func notLoggedIn(w http.ResponseWriter, r *http.Request) {
	switch {
	case strings.HasPrefix(r.URL.Path, "/api/"):
		errorJSON(w, http.StatusUnauthorized, auth.ErrNotAuthenticated.Error())
	case r.Header.Get("HX-Request") == "true":
		// htmx lee HX-Redirect antes de mirar el código: va a /login entero.
		w.Header().Set("HX-Redirect", LoginURL(htmxPage(r)))
		w.WriteHeader(http.StatusUnauthorized)
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		http.Redirect(w, r, LoginURL(r.URL.RequestURI()), http.StatusSeeOther)
	default:
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	}
}

// htmxPage es la página desde donde htmx hizo el pedido (HX-Current-URL),
// para volver a ella después de entrar. Solo el camino: nunca otro sitio.
func htmxPage(r *http.Request) string {
	u, err := url.Parse(r.Header.Get("HX-Current-URL"))
	if err != nil || u.Path == "" {
		return ""
	}
	return u.RequestURI()
}

// LoginURL es /login, con next si sirve para volver.
func LoginURL(next string) string {
	next = SafeNext(next)
	if next == "/" {
		return "/login"
	}
	return "/login?next=" + url.QueryEscape(next)
}

// SafeNext deja volver solo a una página de este sitio: un camino que
// empieza con una sola "/" (no "//otro.sitio" ni "/\otro.sitio", que el
// navegador lee como otro sitio) y no es el login. Si no, "/".
func SafeNext(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") ||
		strings.ContainsAny(next, "\r\n\t") {
		return "/"
	}
	if u, err := url.Parse(next); err != nil || u.Host != "" || u.Scheme != "" || u.Path == "/login" {
		return "/"
	}
	return next
}

// SetSessionCookie guarda el token en el navegador: HttpOnly (el JavaScript
// de la página no lo lee), SameSite=Lax y Secure si el server es HTTPS.
func SetSessionCookie(w http.ResponseWriter, token string, maxAge int, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
}

// ClearSessionCookie borra la cookie de la sesión.
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	SetSessionCookie(w, "", -1, secure)
}

// WithUser pone el usuario que entró en el contexto.
func WithUser(ctx context.Context, u *model.User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// UserID es el id de quién hizo el pedido: los casos de uso lo guardan como
// autor (opened_by, performed_by, audit_log.user_id...). 0 si nadie entró,
// algo que detrás de AuthMiddleware no pasa; y si pasara, la FK lo rechaza
// en lugar de dejar un autor vacío.
func UserID(ctx context.Context) int64 {
	if u := CurrentUser(ctx); u != nil {
		return u.ID
	}
	return 0
}

// CurrentUser es el usuario que hizo el pedido, o nil (en las páginas
// públicas).
func CurrentUser(ctx context.Context) *model.User {
	u, _ := ctx.Value(userKey).(*model.User)
	return u
}
