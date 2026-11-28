package web

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/reports"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/settings"
)

// Redondeo en efectivo (0024): la configuración, la caja, el ticket y el
// reporte.
func TestRenderCashRounding(t *testing.T) {
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
	options := func() []roundingOption {
		out := make([]roundingOption, len(model.CashRoundingMultiples))
		for i, m := range model.CashRoundingMultiples {
			out[i] = roundingOption{Value: m, Amount: decimal.NewFromInt(m)}
		}
		return out
	}

	t.Run("settings: the multiple, from a list", func(t *testing.T) {
		html := render(t, "settings.gohtml", adminCan, map[string]any{
			"Settings": &settings.Settings{CashRounding: 100}, "RoundingOptions": options()})
		mustHave(t, html, `id="settings-rounding"`, `action="/settings/cash-rounding"`, `name="multiple"`,
			`<option value="0" >No redondear</option>`, `<option value="100" selected>Múltiplos de $ 100,00</option>`,
			`<option value="1000" >Múltiplos de $ 1.000,00</option>`)
	})
	t.Run("pos: the step for the screen", func(t *testing.T) {
		html := render(t, "pos.gohtml", adminCan, map[string]any{"Methods": model.PaymentMethods,
			"State": openState(), "CashRounding": int64(100)})
		mustHave(t, html, `data-rounding-step="100"`, `id="pos-cash-total"`, `id="pos-rounding"`, "function posRoundCash")
		off := render(t, "pos.gohtml", adminCan, map[string]any{"Methods": model.PaymentMethods, "State": openState()})
		mustHave(t, off, `data-rounding-step="0"`)
	})
	t.Run("ticket: subtotal and rounding", func(t *testing.T) {
		s := ticketSale()
		s.Subtotal, s.DiscountAmount, s.Total = dec("12450"), dec("50"), dec("12400")
		html := render(t, "ticket.gohtml", nil, map[string]any{"Lang": "es", "Sale": s})
		mustHave(t, html, "data-rounding", "Subtotal", "$ 12.450,00", "Redondeo efectivo", "-$ 50,00", "$ 12.400,00")
		plain := render(t, "ticket.gohtml", nil, map[string]any{"Lang": "es", "Sale": ticketSale()})
		if strings.Contains(plain, "data-rounding") {
			t.Error("a sale without rounding shows the rounding line")
		}
	})
	t.Run("report", func(t *testing.T) {
		march := time.Date(2031, 3, 1, 0, 0, 0, 0, time.UTC)
		html := render(t, "report_rounding.gohtml", adminCan, map[string]any{"Section": "reports_rounding",
			"TitleKey": "report.rounding_title", "Multiple": dec("100"),
			"Rows": []roundingRow{{Label: "marzo 2031", RoundingMonth: reports.RoundingMonth{Start: march, Rounded: 2,
				Amount: dec("90"), CashSales: 5}}},
			"Total": reports.RoundingMonth{Rounded: 2, Amount: dec("90"), CashSales: 5}})
		mustHave(t, html, `href="/reports/rounding" aria-current="page"`, "múltiplos de $ 100,00", "marzo 2031",
			`id="rounding-total"`, "$ 90,00")
		off := render(t, "report_rounding.gohtml", adminCan, map[string]any{"Multiple": decimal.Zero,
			"Total": reports.RoundingMonth{}})
		mustHave(t, off, "Hoy no se redondea")
	})
	t.Run("only who sees reports has the link", func(t *testing.T) {
		html := render(t, "report_unstocked.gohtml", stockCan, map[string]any{})
		if strings.Contains(html, `href="/reports/rounding"`) {
			t.Error("stock clerk sees the rounding report link")
		}
	})
}

// A granel por importe (PR11): la caja lo pasa a kilos; el servidor no
// cambia.
func TestRenderPosByAmount(t *testing.T) {
	e := NewTemplateEngine()
	var buf bytes.Buffer
	if err := e.Render(&buf, "es", "pos.gohtml", map[string]any{"Methods": model.SaleMethods, "State": openState(),
		"Can": adminCan}); err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, s := range []string{"data-by-amount", "data-mode-toggle", "function posAmountToKg",
		"Math.round(cents * 1000 / price)", `data-kg-note="= %s kg"`} {
		if !strings.Contains(html, s) {
			t.Errorf("missing %q", s)
		}
	}
}
