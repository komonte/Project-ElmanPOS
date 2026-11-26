package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

func TestGuardRequire(t *testing.T) {
	var called, pageCalled bool
	g := Guard{Page: func(w http.ResponseWriter, r *http.Request) {
		pageCalled = true
		w.WriteHeader(http.StatusForbidden)
	}}
	h := g.Require(model.PermVoid, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusTeapot)
	}))
	admin := &model.User{ID: 1, Username: "admin", Role: model.RoleAdmin, IsActive: true}
	cashier := &model.User{ID: 2, Username: "cajera", Role: model.RoleCashier, IsActive: true}
	do := func(u *model.User, method, target string, headers map[string]string) *httptest.ResponseRecorder {
		called, pageCalled = false, false
		r := httptest.NewRequest(method, target, nil)
		ctx := context.WithValue(r.Context(), localeKey, "es")
		if u != nil {
			ctx = WithUser(ctx, u)
		}
		r = r.WithContext(ctx)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	t.Run("with the permission it passes", func(t *testing.T) {
		if w := do(admin, "POST", "/cash/sales/3/void", nil); w.Code != http.StatusTeapot || !called {
			t.Errorf("got %d", w.Code)
		}
	})

	t.Run("page: the 403 page", func(t *testing.T) {
		w := do(cashier, "POST", "/cash/sales/3/void", nil)
		if called || !pageCalled || w.Code != http.StatusForbidden {
			t.Errorf("got %d, handler %v, page %v", w.Code, called, pageCalled)
		}
	})

	t.Run("htmx: the message as text", func(t *testing.T) {
		w := do(cashier, "POST", "/cash/sales/3/void", map[string]string{"HX-Request": "true"})
		if called || pageCalled || w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "No tenés permiso") {
			t.Errorf("got %d %q", w.Code, w.Body.String())
		}
	})

	t.Run("API: 403 JSON", func(t *testing.T) {
		w := do(cashier, "GET", "/api/brands", nil)
		if called || pageCalled || w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"forbidden"`) ||
			!strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
			t.Errorf("got %d %q", w.Code, w.Body.String())
		}
	})

	t.Run("inactive user: no permission", func(t *testing.T) {
		gone := *admin
		gone.IsActive = false
		if w := do(&gone, "POST", "/cash/sales/3/void", nil); called || w.Code != http.StatusForbidden {
			t.Errorf("got %d", w.Code)
		}
	})

	t.Run("nobody logged in: goes to login", func(t *testing.T) {
		w := do(nil, "GET", "/cash", nil)
		if called || w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login?next=%2Fcash" {
			t.Errorf("got %d %q", w.Code, w.Header().Get("Location"))
		}
	})

	t.Run("no page: the message as text", func(t *testing.T) {
		h := Guard{}.Require(model.PermVoid, http.NotFoundHandler())
		r := httptest.NewRequest("GET", "/cash", nil)
		r = r.WithContext(WithUser(context.WithValue(r.Context(), localeKey, "en"), cashier))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "permission") {
			t.Errorf("got %d %q", w.Code, w.Body.String())
		}
	})
}
