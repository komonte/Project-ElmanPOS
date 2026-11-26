package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/auth"
)

func TestSafeNext(t *testing.T) {
	tests := map[string]string{
		"":                       "/",
		"/cash":                  "/cash",
		"/cash/sessions/7?x=1":   "/cash/sessions/7?x=1",
		"/":                      "/",
		"cash":                   "/",
		"//evil.example/x":       "/",
		"/\\evil.example":        "/",
		"https://evil.example/":  "/",
		"/login":                 "/",
		"/login?next=/cash":      "/",
		"/cash\r\nSet-Cookie: x": "/",
		"javascript:alert(1)":    "/",
	}
	for in, want := range tests {
		if got := SafeNext(in); got != want {
			t.Errorf("SafeNext(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLoginURL(t *testing.T) {
	for in, want := range map[string]string{
		"/":                   "/login",
		"":                    "/login",
		"//evil.example":      "/login",
		"/cash?voided=12":     "/login?next=%2Fcash%3Fvoided%3D12",
		"/pos/sales/3/ticket": "/login?next=%2Fpos%2Fsales%2F3%2Fticket",
	} {
		if got := LoginURL(in); got != want {
			t.Errorf("LoginURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// fakeAuth: "good" es la única sesión; "boom" simula la base caída.
type fakeAuth struct{}

func (fakeAuth) Authenticate(_ context.Context, token string) (*model.UserSession, error) {
	switch token {
	case "good":
		return &model.UserSession{ID: 1, User: &model.User{ID: 7, Username: "cajera", Role: model.RoleCashier}}, nil
	case "boom":
		return nil, errors.New("connection refused")
	}
	return nil, auth.ErrNotAuthenticated
}

func TestAuthMiddleware(t *testing.T) {
	var seen *model.User
	var called bool
	h := AuthMiddleware(fakeAuth{}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called, seen = true, CurrentUser(r.Context())
		w.WriteHeader(http.StatusTeapot)
	}))
	do := func(method, target, cookie string, headers map[string]string) *httptest.ResponseRecorder {
		called, seen = false, nil
		r := httptest.NewRequest(method, target, nil)
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: SessionCookie, Value: cookie})
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	t.Run("public paths without a session", func(t *testing.T) {
		for _, p := range []string{"/login", "/static/css/output.css", "/settings/language"} {
			if w := do("GET", p, "", nil); w.Code != http.StatusTeapot || !called || seen != nil {
				t.Errorf("%s: got %d", p, w.Code)
			}
		}
	})

	t.Run("valid session: the user is in the context", func(t *testing.T) {
		w := do("POST", "/cash/close", "good", nil)
		if w.Code != http.StatusTeapot || seen == nil || seen.ID != 7 {
			t.Errorf("got %d, user %+v", w.Code, seen)
		}
	})

	t.Run("page without a session goes to login and comes back", func(t *testing.T) {
		w := do("GET", "/cash?voided=12", "", nil)
		if called || w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?next=%2Fcash%3Fvoided%3D12" {
			t.Errorf("got %d %q", w.Code, w.Header().Get("Location"))
		}
		if w.Header().Get("Set-Cookie") != "" {
			t.Error("no cookie to clear")
		}
	})

	t.Run("expired session: the cookie is cleared", func(t *testing.T) {
		w := do("GET", "/", "old", nil)
		if called || w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
			t.Errorf("got %d %q", w.Code, w.Header().Get("Location"))
		}
		c := w.Header().Get("Set-Cookie")
		if !strings.HasPrefix(c, SessionCookie+"=;") || !strings.Contains(c, "Max-Age=0") {
			t.Errorf("cookie not cleared: %q", c)
		}
	})

	t.Run("htmx: HX-Redirect to the page it came from", func(t *testing.T) {
		w := do("POST", "/pos/checkout", "", map[string]string{"HX-Request": "true",
			"HX-Current-URL": "http://192.168.0.10:8080/pos?x=1"})
		if called || w.Code != http.StatusUnauthorized || w.Header().Get("HX-Redirect") != "/login?next=%2Fpos%3Fx%3D1" {
			t.Errorf("got %d %q", w.Code, w.Header().Get("HX-Redirect"))
		}
		// HX-Current-URL de otro sitio: solo se usa el camino.
		w = do("GET", "/pos/search", "", map[string]string{"HX-Request": "true", "HX-Current-URL": "https://evil.example//x"})
		if w.Header().Get("HX-Redirect") != "/login" {
			t.Errorf("foreign current url: got %q", w.Header().Get("HX-Redirect"))
		}
	})

	t.Run("API: 401 JSON", func(t *testing.T) {
		w := do("GET", "/api/brands", "", nil)
		if called || w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "not logged in") ||
			!strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			t.Errorf("got %d %q", w.Code, w.Body.String())
		}
	})

	t.Run("form POST without a session goes to login", func(t *testing.T) {
		w := do("POST", "/cash/close", "", nil)
		if called || w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
			t.Errorf("got %d %q", w.Code, w.Header().Get("Location"))
		}
	})

	t.Run("database down: 500, not the login", func(t *testing.T) {
		w := do("GET", "/cash", "boom", nil)
		if called || w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "refused") {
			t.Errorf("got %d %q", w.Code, w.Body.String())
		}
	})
}

func TestSessionCookie(t *testing.T) {
	w := httptest.NewRecorder()
	SetSessionCookie(w, "tok", 3600, true)
	c := w.Header().Get("Set-Cookie")
	for _, want := range []string{SessionCookie + "=tok", "Path=/", "Max-Age=3600", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(c, want) {
			t.Errorf("missing %q in %q", want, c)
		}
	}
	w = httptest.NewRecorder()
	SetSessionCookie(w, "tok", 3600, false)
	if strings.Contains(w.Header().Get("Set-Cookie"), "Secure") {
		t.Error("plain HTTP: no Secure (the browser would drop the cookie)")
	}
}
