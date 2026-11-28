package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sessions"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/settings"
)

// Modo arranque: la configuración, la lista de vendidos sin stock, el aviso
// del panel y la caja.
func TestRenderStartupMode(t *testing.T) {
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

	t.Run("settings off", func(t *testing.T) {
		html := render(t, "settings.gohtml", adminCan, map[string]any{"Section": "settings", "TitleKey": "settings.title",
			"Settings": &settings.Settings{}, "Saved": true})
		mustHave(t, html, `href="/settings" aria-current="page"`, `data-on="false"`, `name="startup_mode" value="on"`,
			"Prender el modo arranque", `id="settings-saved"`)
	})
	t.Run("settings on, with pending", func(t *testing.T) {
		html := render(t, "settings.gohtml", adminCan, map[string]any{"Settings": &settings.Settings{StartupMode: true},
			"UnstockedCount": 4})
		mustHave(t, html, `data-on="true"`, "Apagar el modo arranque", "Todavía hay 4 productos")
		if strings.Contains(html, `name="startup_mode"`) {
			t.Error("turning off must not send startup_mode=on")
		}
	})
	t.Run("only the admin sees Settings in the menu", func(t *testing.T) {
		html := render(t, "report_unstocked.gohtml", stockCan, map[string]any{})
		if strings.Contains(html, `href="/settings"`) {
			t.Error("stock clerk sees the settings link")
		}
		mustHave(t, html, `href="/reports/unstocked"`, `id="report-empty"`)
	})

	t.Run("unstocked list", func(t *testing.T) {
		dec := decimal.RequireFromString
		html := render(t, "report_unstocked.gohtml", stockCan, map[string]any{"Section": "reports_unstocked",
			"TitleKey": "report.unstocked_title", "Items": []model.UnstockedItem{
				{ProductID: 5, SKU: "000005", Name: "OREO", SaleType: model.SaleTypeUnit, Uncovered: dec("3"), Sales: 2,
					FirstSold: "2026-11-28T12:00:00Z", LastSold: "2026-11-28T15:00:00Z", OnHand: dec("0")},
				{ProductID: 6, SKU: "000006", Name: "QUESO", SaleType: model.SaleTypeWeight, Uncovered: dec("0.25"), Sales: 1,
					FirstSold: "2026-11-28T13:00:00Z", LastSold: "2026-11-28T13:00:00Z", OnHand: dec("1.5")},
			}})
		mustHave(t, html, "<title>ElmanPOS - Vendidos sin stock</title>", `href="/reports/unstocked" aria-current="page"`,
			`data-product="5"`, "OREO", "0,25 kg", "1,5 kg", "2026-11-28 12:00:00", `id="unstocked-count" href="/counts"`)
	})

	t.Run("dashboard banner", func(t *testing.T) {
		html := render(t, "index.gohtml", stockCan, map[string]any{"StartupMode": true, "UnstockedCount": 1})
		mustHave(t, html, `id="startup-banner"`, "Modo arranque prendido", "1 producto vendido sin stock para contar")
		html = render(t, "index.gohtml", stockCan, map[string]any{})
		if strings.Contains(html, `id="startup-banner"`) {
			t.Error("banner without startup mode or pending")
		}
	})

	t.Run("pos knows the mode", func(t *testing.T) {
		open := &sessions.State{Register: &model.CashRegister{ID: 1, Name: "Caja 1"}, Session: &model.CashierSession{ID: 3}}
		html := render(t, "pos.gohtml", adminCan, map[string]any{"Methods": model.PaymentMethods, "State": open,
			"StartupMode": true})
		mustHave(t, html, `data-startup="1"`, "el resto queda para contar")
		html = render(t, "pos.gohtml", adminCan, map[string]any{"Methods": model.PaymentMethods, "State": open})
		mustHave(t, html, `data-startup=""`)
	})
}
