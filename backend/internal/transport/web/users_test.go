package web

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/users"
)

// La página de usuarios (F-13, PR F).
func TestRenderUsers(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, name string, data map[string]any) string {
		t.Helper()
		data["Can"] = adminCan
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", name, data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	me := &model.User{ID: 1, Username: "admin", Name: "La Jefa", Role: model.RoleAdmin, IsActive: true}
	ana := &model.User{ID: 2, Username: "ana", Name: "Ana", Role: model.RoleCashier, IsActive: true}
	old := &model.User{ID: 3, Username: "beto", Name: "Beto", Role: model.RoleStockClerk}

	t.Run("table", func(t *testing.T) {
		html := render(t, "users.gohtml", map[string]any{"Section": "users", "TitleKey": "user.title",
			"Users": userRows([]*model.User{me, ana, old}, me.ID)})
		for _, s := range []string{"<title>ElmanPOS - Usuarios</title>", `hx-get="/users/new"`,
			`La Jefa <span class="font-normal text-muted">(vos)</span>`, "Cajero", "Repositor",
			`hx-patch="/users/2/deactivate"`, `hx-patch="/users/3/reactivate"`,
			`hx-get="/users/1/edit"`, `hx-get="/users/1/password"`, `hx-get="/users/3/password"`,
			`href="/users" aria-current="page"`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		// Uno mismo no se desactiva; nadie se borra.
		for _, s := range []string{`hx-patch="/users/1/deactivate"`, `hx-patch="/users/2/reactivate"`, "hx-delete"} {
			if strings.Contains(html, s) {
				t.Errorf("unexpected %q", s)
			}
		}
	})

	t.Run("new form", func(t *testing.T) {
		html := render(t, "user_form.gohtml", map[string]any{"Roles": model.Roles, "PasswordMinLen": 8})
		for _, s := range []string{`hx-post="/users/new"`, `name="username"`, `name="name"`, `<select id="user-role" name="role"`,
			`name="password" required minlength="8"`, `name="password_again"`, "Al menos 8 caracteres.",
			`<option value="CASHIER" >Cajero</option>`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})

	t.Run("edit another user", func(t *testing.T) {
		html := render(t, "user_form.gohtml", map[string]any{"User": ana, "Roles": model.Roles, "Error": "Algo"})
		for _, s := range []string{`hx-post="/users/2/edit"`, `id="user-username" class="text-lg">ana</p>`,
			`value="Ana"`, `<select id="user-role" name="role"`, `<option value="CASHIER" selected>Cajero</option>`,
			`role="alert"`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		for _, s := range []string{`name="username"`, `name="password"`, `type="hidden" name="role"`} {
			if strings.Contains(html, s) {
				t.Errorf("unexpected %q", s)
			}
		}
	})

	t.Run("edit yourself: the role is fixed", func(t *testing.T) {
		html := render(t, "user_form.gohtml", map[string]any{"User": me, "Self": true, "Roles": model.Roles})
		for _, s := range []string{`<select id="user-role" disabled aria-describedby="user-role-self"`,
			`<input type="hidden" name="role" value="ADMIN">`, "No podés cambiar tu propio rol"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})

	t.Run("password", func(t *testing.T) {
		html := render(t, "user_password.gohtml", map[string]any{"User": ana, "PasswordMinLen": 8})
		for _, s := range []string{`hx-post="/users/2/password"`, `name="password"`, `name="password_again"`,
			"Si tiene una sesión abierta, se cierra"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		self := render(t, "user_password.gohtml", map[string]any{"User": me, "Self": true, "PasswordMinLen": 8})
		if !strings.Contains(self, "Es tu propia contraseña") {
			t.Error("own password: missing the warning")
		}
		done := render(t, "user_password.gohtml", map[string]any{"User": ana, "Done": true})
		if !strings.Contains(done, `id="password-done"`) || strings.Contains(done, "<form") {
			t.Errorf("done: got %s", done)
		}
	})
}

// Sin idioma en el pedido, los mensajes salen en inglés.
func TestUserErrMsg(t *testing.T) {
	h := &WebHandler{}
	r := httptest.NewRequest("GET", "/users", nil)
	for err, want := range map[error]string{
		fmt.Errorf("%w: %w", service.ErrAlreadyExists, store.ErrDuplicateUsername): "There is already a user with that username.",
		fmt.Errorf("%w: %w", service.ErrValidation, model.ErrInvalidUsername):      "The username must have 3 to 60",
		fmt.Errorf("%w: %w", service.ErrValidation, model.ErrPasswordTooShort):     "at least 8 characters",
		errPasswordMismatch:     "The two passwords do not match.",
		users.ErrSelfDeactivate: "You cannot deactivate yourself.",
		users.ErrSelfRole:       "You cannot change your own role",
		users.ErrLastAdmin:      "At least one administrator must stay active.",
	} {
		if got := h.errMsg(r, err); !strings.Contains(got, want) {
			t.Errorf("%v: got %q, want %q", err, got, want)
		}
	}
}
