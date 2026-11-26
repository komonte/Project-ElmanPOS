package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sessions"
)

func TestRenderLogin(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, data map[string]any) string {
		t.Helper()
		data["TitleKey"], data["HideNav"], data["Section"] = "login.title", true, ""
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", "login.gohtml", data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}

	t.Run("empty", func(t *testing.T) {
		html := render(t, map[string]any{"Next": "/cash"})
		for _, s := range []string{"<title>ElmanPOS - Entrar</title>", `action="/login"`, `method="post"`,
			`name="next" value="/cash"`, `name="username" value="" required`, `autocomplete="username"`,
			`autocapitalize="none"`, `class="field text-lg" autofocus>`,
			`type="password" id="login-password" name="password" required autocomplete="current-password"`,
			"Escribí tu usuario y tu contraseña.", `hx-post="/settings/language"`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		// Sin menú: sin sesión no hay a dónde ir.
		for _, s := range []string{`href="/pos"`, `href="/cash"`, "nav-user", `role="alert" class="p-4`} {
			if strings.Contains(html, s) {
				t.Errorf("unexpected %q", s)
			}
		}
	})

	t.Run("failed", func(t *testing.T) {
		html := render(t, map[string]any{"Next": "/", "Username": "cajera",
			"Error": "Usuario o contraseña incorrectos."})
		for _, s := range []string{`role="alert" class="p-4`, "Usuario o contraseña incorrectos.", `value="cajera"`,
			`autocomplete="current-password"
             class="field text-lg" autofocus>`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		// El foco va a la contraseña, no al usuario ya escrito.
		if strings.Contains(html, `class="field text-lg" autofocus>
    </div>
    <div>
      <label for="login-password"`) {
			t.Error("focus must go to the password")
		}
	})
}

func TestRenderNavUser(t *testing.T) {
	e := NewTemplateEngine()
	var buf bytes.Buffer
	err := e.Render(&buf, "es", "cash.gohtml", map[string]any{"Section": "cash", "TitleKey": "cash.title",
		"State":       &sessions.State{},
		"CurrentUser": &model.User{ID: 3, Username: "cajera", Name: "Ana López", Role: model.RoleCashier},
		"Can":         canMap(cashierUser)})
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, s := range []string{`id="nav-user"`, "Ana López", "Cajero", `<form method="post" action="/logout"`,
		`id="logout"`, ">Salir</button>"} {
		if !strings.Contains(html, s) {
			t.Errorf("missing %q", s)
		}
	}

	// Sin usuario (no pasa con el middleware, pero el menú no se rompe): solo
	// el inicio, sin permisos.
	buf.Reset()
	if err := e.Render(&buf, "es", "cash.gohtml", map[string]any{"Section": "cash", "TitleKey": "cash.title",
		"State": &sessions.State{}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "nav-user") || !strings.Contains(buf.String(), `href="/"`) ||
		strings.Contains(buf.String(), `href="/pos"`) {
		t.Error("no user: menu without the user block")
	}
}
