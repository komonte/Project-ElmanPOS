package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/auth"
)

// Login (F-13). La página no tiene menú (sin sesión no hay a dónde ir) y
// ofrece el idioma. Después de entrar vuelve a la página que se quiso
// abrir (next), siempre dentro de este sitio (transport.SafeNext).

// LoginPage handles GET /login. Con una sesión que vale, no pide entrar de
// nuevo: va a next.
func (h *WebHandler) LoginPage(w http.ResponseWriter, r *http.Request) {
	next := transport.SafeNext(r.FormValue("next"))
	if c, err := r.Cookie(transport.SessionCookie); err == nil {
		if _, err := h.auth.Authenticate(r.Context(), c.Value); err == nil {
			http.Redirect(w, r, next, http.StatusSeeOther)
			return
		}
	}
	h.renderLogin(w, r, http.StatusOK, map[string]any{"Next": next})
}

// LoginSubmit handles POST /login: usuario, contraseña y next. Bien: guarda
// la cookie y va a next. Mal: la página vuelve con el usuario escrito (la
// contraseña nunca) y un mensaje que no dice qué estuvo mal.
func (h *WebHandler) LoginSubmit(w http.ResponseWriter, r *http.Request) {
	username, next := r.FormValue("username"), transport.SafeNext(r.FormValue("next"))
	login, err := h.auth.Login(r.Context(), username, r.FormValue("password"))
	if err != nil {
		key := "error.login_invalid"
		switch {
		case errors.Is(err, auth.ErrTooManyAttempts):
			key = "error.login_locked"
		case !errors.Is(err, auth.ErrInvalidCredentials):
			key = "error.unexpected"
		}
		h.renderLogin(w, r, http.StatusUnauthorized, map[string]any{"Next": next, "Username": username,
			"Error": transport.T(r.Context(), key)})
		return
	}
	transport.SetSessionCookie(w, login.Token, int(time.Until(login.ExpiresAt).Seconds()), h.secureCookie)
	if next == "/" {
		next = homeFor(login.User) // el cajero, directo a vender
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// Logout handles POST /logout: cierra la sesión de este navegador y vuelve
// al login.
func (h *WebHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(transport.SessionCookie); err == nil {
		if err := h.auth.Logout(r.Context(), c.Value); err != nil {
			http.Error(w, h.errMsg(r, err), http.StatusInternalServerError)
			return
		}
	}
	transport.ClearSessionCookie(w, h.secureCookie)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *WebHandler) renderLogin(w http.ResponseWriter, r *http.Request, status int, data map[string]any) {
	data["TitleKey"], data["HideNav"] = "login.title", true
	// No guardar la página del login en una caché, con el usuario escrito.
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	h.render(w, r, "login.gohtml", data)
}
