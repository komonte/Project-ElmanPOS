package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

// Clientes (0025): la tabla, el formulario y el menú.
func TestRenderCustomers(t *testing.T) {
	e := NewTemplateEngine()
	render := func(t *testing.T, name string, can map[string]bool, data map[string]any) string {
		t.Helper()
		data["Can"] = can
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", name, data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	mustHave := func(t *testing.T, html string, want ...string) {
		t.Helper()
		for _, s := range want {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	}
	list := []*model.Customer{
		{ID: 3, Name: "María (kiosco)", Phone: "11 5555", Notes: "paga los viernes", IsActive: true},
		{ID: 4, Name: "Juan", IsActive: false},
	}

	t.Run("page", func(t *testing.T) {
		html := render(t, "customers.gohtml", adminCan, map[string]any{"Section": "customers",
			"TitleKey": "customer.title", "Customers": list, "Search": "ma"})
		mustHave(t, html, `href="/customers" aria-current="page"`, `id="customer-new"`, `value="ma"`,
			"María (kiosco)", "11 5555", "paga los viernes", `hx-patch="/customers/3/deactivate"`,
			`hx-patch="/customers/4/reactivate"`, `hx-get="/customers/3/edit"`)
	})
	t.Run("empty", func(t *testing.T) {
		html := render(t, "customer_table.gohtml", adminCan, map[string]any{})
		mustHave(t, html, `id="customers-empty"`)
	})
	t.Run("form: new and edit keep what was typed", func(t *testing.T) {
		html := render(t, "customer_form.gohtml", adminCan, map[string]any{"Error": "Ya hay un cliente con ese nombre.",
			"Customer": &model.Customer{Name: "María", Phone: "11"}})
		mustHave(t, html, `hx-post="/customers/new"`, `value="María"`, `value="11"`, `role="alert"`)
		edit := render(t, "customer_form.gohtml", adminCan, map[string]any{"Customer": list[0]})
		mustHave(t, edit, `hx-post="/customers/3/edit"`, "paga los viernes</textarea>")
	})
	t.Run("only the admin has the menu link", func(t *testing.T) {
		admin := render(t, "customers.gohtml", adminCan, map[string]any{"Customers": list})
		mustHave(t, admin, `href="/customers"`)
		for _, can := range []map[string]bool{canMap(cashierUser), stockCan} {
			if html := render(t, "report_unstocked.gohtml", can, map[string]any{}); strings.Contains(html, `href="/customers"`) {
				t.Error("a non-admin sees the customers link")
			}
		}
	})
}
