package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sessions"
)

var (
	adminUser   = &model.User{ID: 1, Username: "admin", Name: "Admin", Role: model.RoleAdmin, IsActive: true}
	cashierUser = &model.User{ID: 2, Username: "cajero", Name: "Cajera", Role: model.RoleCashier, IsActive: true}
	stockUser   = &model.User{ID: 3, Username: "stock", Name: "Repositor", Role: model.RoleStockClerk, IsActive: true}

	adminCan = canMap(adminUser)
	stockCan = canMap(stockUser)
)

func TestCanMap(t *testing.T) {
	c := canMap(cashierUser)
	if !c["sell"] || !c["cash"] || c["void"] || c["prices"] || len(c) != 2 {
		t.Errorf("cashier: got %v", c)
	}
	if len(canMap(nil)) != 0 {
		t.Error("nobody: no permissions")
	}
}

func TestHomeFor(t *testing.T) {
	for u, want := range map[*model.User]string{adminUser: "/", cashierUser: "/pos", stockUser: "/", nil: "/"} {
		if got := homeFor(u); got != want {
			t.Errorf("homeFor(%v) = %q, want %q", u, got, want)
		}
	}
}

// TestNavByRole: el menú muestra solo lo que el rol puede abrir.
func TestNavByRole(t *testing.T) {
	e := NewTemplateEngine()
	links := []string{`href="/pos"`, `href="/products"`, `href="/categories"`, `href="/brands"`, `href="/taxes"`,
		`href="/suppliers"`, `href="/purchases"`, `href="/purchases/history"`, `href="/counts"`, `href="/adjustments"`, `href="/cash"`,
		`href="/reports/sales"`, `href="/reports/cash"`, `href="/reports/restock"`, `href="/reports/expiring"`, `href="/users"`}
	want := map[*model.User][]string{
		adminUser:   links,
		cashierUser: {`href="/pos"`, `href="/cash"`},
		stockUser: {`href="/products"`, `href="/categories"`, `href="/brands"`, `href="/suppliers"`,
			`href="/purchases"`, `href="/purchases/history"`, `href="/counts"`, `href="/adjustments"`, `href="/reports/restock"`, `href="/reports/expiring"`},
	}
	for u, shown := range want {
		var buf bytes.Buffer
		data := map[string]any{"Lang": "es", "Section": "", "TitleKey": "", "CurrentUser": u, "Can": canMap(u)}
		if err := e.Render(&buf, "es", "index.gohtml", data); err != nil {
			t.Fatal(err)
		}
		html := buf.String()
		for _, l := range links {
			in := false
			for _, s := range shown {
				in = in || s == l
			}
			// El inicio también tiene links al catálogo: se cuenta el menú.
			nav := html[:strings.Index(html, `id="nav-user"`)]
			if strings.Contains(nav, l) != in {
				t.Errorf("%s: link %s shown = %v, want %v", u.Role, l, !in, in)
			}
		}
		if !strings.Contains(html, `href="/"`) {
			t.Errorf("%s: no link to the start page", u.Role)
		}
	}
}

func TestCashPageWithoutRegister(t *testing.T) {
	e := NewTemplateEngine()
	render := func(u *model.User) string {
		var buf bytes.Buffer
		data := map[string]any{"Lang": "es", "Section": "cash", "TitleKey": "", "CurrentUser": u, "Can": canMap(u),
			"State": &sessions.State{}}
		if err := e.Render(&buf, "es", "cash.gohtml", data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	if html := render(adminUser); !strings.Contains(html, `action="/cash/register"`) {
		t.Error("admin: no button to create the register")
	}
	if html := render(cashierUser); strings.Contains(html, `action="/cash/register"`) ||
		!strings.Contains(html, "administrador") {
		t.Error("cashier: creates the register or no hint to ask the admin")
	}
}

func TestForbiddenPage(t *testing.T) {
	h := &WebHandler{engine: NewTemplateEngine()}
	r := httptest.NewRequest("GET", "/taxes", nil)
	r = r.WithContext(transport.WithUser(r.Context(), cashierUser))
	w := httptest.NewRecorder()
	h.Forbidden(w, r)
	html := w.Body.String()
	// Sin idioma en el contexto sale en inglés. "Back" lleva a vender.
	for _, s := range []string{"Cashier", `<a href="/pos" class="btn-primary">`} {
		if !strings.Contains(html, s) {
			t.Errorf("missing %q", s)
		}
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("got %d", w.Code)
	}
}
