package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sessions"
)

// Quién hizo qué (F-13) en las pantallas: el nombre, o "—" si fue antes del
// login.
func TestRenderWho(t *testing.T) {
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
	ana := "Ana López"
	dec := decimal.RequireFromString

	t.Run("close summary: who opened and who closed", func(t *testing.T) {
		sess := &model.CashierSession{ID: 4, RegisterName: "Caja 1", OpeningAmount: dec("0"),
			OpenedAt: "2026-11-26T12:00:00Z", ClosedByName: &ana}
		even, _ := model.NewCloseSummary(dec("0"), nil, nil)
		html := render(t, "cash_session.gohtml", map[string]any{"Section": "cash", "TitleKey": "cash.summary_title",
			"Summary": &sessions.Summary{Session: sess, Close: even, Total: dec("0")}, "ClosedAt": "2026-11-26T23:30:00Z",
			"Rows": closeRows(even), "HasDifference": false})
		want := `Abrió <span class="font-bold text-ink">—</span> ·
    Cerró <span class="font-bold text-ink">Ana López</span>`
		if !strings.Contains(html, want) {
			t.Errorf("missing who opened and closed in %s", html)
		}
	})

	t.Run("open register: who opened and who charged each sale", func(t *testing.T) {
		state := &sessions.State{Register: &model.CashRegister{ID: 1, Name: "Caja 1"}, Session: &model.CashierSession{
			ID: 4, OpeningAmount: dec("0"), OpenedAt: "2026-11-26T12:00:00Z", OpenedByName: &ana}}
		list := []*model.Sale{
			{ID: 31, SaleNumber: 2, Status: model.SaleCompleted, Total: dec("100"), CreatedAt: "2026-11-26T13:00:00Z",
				CreatedByName: &ana},
			{ID: 30, SaleNumber: 1, Status: model.SaleCompleted, Total: dec("100"), CreatedAt: "2026-11-26T12:30:00Z"},
		}
		html := render(t, "cash.gohtml", map[string]any{"Section": "cash", "TitleKey": "cash.title",
			"State": state, "Sales": list})
		for _, s := range []string{`id="cash-opened-by">Abrió <span class="font-bold">Ana López</span>`,
			`<th scope="col" class="p-3">Cobró</th>`,
			`<td class="p-3" data-charged-by>Ana López</td>`, `<td class="p-3" data-charged-by>—</td>`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})

	t.Run("recent exits: who wrote each one", func(t *testing.T) {
		html := render(t, "adjustment_recent.gohtml", map[string]any{"Adjustments": []*model.StockAdjustment{
			{ID: 1, ProductName: "YERBA", Reason: model.ReasonDamage, Quantity: dec("1"), CreatedAt: "2026-11-26T12:00:00Z",
				CreatedByName: &ana},
			{ID: 2, ProductName: "SODA", Reason: model.ReasonDamage, Quantity: dec("1"), CreatedAt: "2026-11-26T11:00:00Z"},
		}})
		for _, s := range []string{">Anotó</th>", `whitespace-nowrap">Ana López</td>`, `whitespace-nowrap">—</td>`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})
}
