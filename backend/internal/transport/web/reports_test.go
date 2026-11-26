package web

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/reports"
)

func TestShortcuts(t *testing.T) {
	ba, _ := time.LoadLocation("America/Argentina/Buenos_Aires")
	today := time.Date(2026, 3, 1, 0, 0, 0, 0, ba)
	p, _ := model.ParsePeriod("2026-02-01", "2026-02-28", ba, today)
	got := shortcuts(today, &p)
	want := []shortcut{
		{"report.today", "2026-03-01", "2026-03-01", false},
		{"report.yesterday", "2026-02-28", "2026-02-28", false},
		{"report.last_7_days", "2026-02-23", "2026-03-01", false},
		{"report.this_month", "2026-03-01", "2026-03-01", false},
		{"report.last_month", "2026-02-01", "2026-02-28", true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestBucketLabel(t *testing.T) {
	ba, _ := time.LoadLocation("America/Argentina/Buenos_Aires")
	at := time.Date(2031, 3, 10, 9, 0, 0, 0, ba) // un lunes
	for _, c := range []struct {
		lang string
		g    model.Granularity
		want string
	}{
		{"es", model.ByHour, "09:00"},
		{"es", model.ByDay, "lun 10/03/2031"},
		{"en", model.ByDay, "Mon 2031-03-10"},
		{"es", model.ByMonth, "marzo 2031"},
		{"en", model.ByMonth, "March 2031"},
	} {
		if got := bucketLabel(c.lang, c.g, at); got != c.want {
			t.Errorf("%s %s: got %q, want %q", c.lang, c.g, got, c.want)
		}
	}
}

func TestReportPeriodFromRequest(t *testing.T) {
	old := reportNow
	reportNow = func() time.Time { return time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { reportNow = old })
	h := &WebHandler{}

	p, filter, msg := h.reportPeriod(httptest.NewRequest("GET", "/reports/sales", nil), "/reports/sales")
	if p == nil || msg != "" || filter.From != "2026-11-26" || filter.To != "2026-11-26" || !filter.Shortcuts[0].Active {
		t.Errorf("default is today: %+v %+v %q", p, filter, msg)
	}
	p, filter, msg = h.reportPeriod(httptest.NewRequest("GET", "/reports/sales?from=2026-11-27&to=2026-11-20", nil), "/reports/sales")
	if p != nil || msg != "The “from” date is after the “to” date." || filter.From != "2026-11-27" || filter.To != "2026-11-20" {
		t.Errorf("reversed: %+v %+v %q", p, filter, msg)
	}
}

func TestRenderReports(t *testing.T) {
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
	ba, _ := time.LoadLocation("America/Argentina/Buenos_Aires")
	dec := decimal.RequireFromString
	filter := periodFilter{Action: "/reports/sales", From: "2031-03-10", To: "2031-03-10",
		Shortcuts: []shortcut{{"report.today", "2031-03-10", "2031-03-10", true}}}

	t.Run("sales", func(t *testing.T) {
		row := reports.Row{Start: time.Date(2031, 3, 10, 9, 0, 0, 0, ba), Count: 2, Total: dec("500"), Units: dec("5"),
			Kg: dec("0"), ByMethod: map[string]decimal.Decimal{"CASH": dec("350"), "TRANSFER": dec("150")}}
		empty := reports.Row{Start: time.Date(2031, 3, 10, 10, 0, 0, 0, ba), ByMethod: map[string]decimal.Decimal{}}
		rep := &reports.Sales{Granularity: model.ByHour, Rows: []reports.Row{row, empty},
			Total: reports.Row{Count: 2, Total: dec("500"), Units: dec("5"), Kg: dec("1.25"), ByMethod: row.ByMethod},
			Methods: []reports.Method{{Method: "CASH", Count: 2, Amount: dec("350"), Share: dec("70")},
				{Method: "TRANSFER", Count: 1, Amount: dec("150"), Share: dec("30")}},
			Voided: 1, VoidedTotal: dec("700")}
		html := render(t, "report_sales.gohtml", map[string]any{"Section": "reports_sales", "TitleKey": "report.sales_title",
			"Filter": filter, "Report": rep, "Rows": salesRows("es", rep), "TotalMethods": byColumn(rep, rep.Total)})
		for _, s := range []string{"<title>ElmanPOS - Ventas</title>", `href="/reports/sales" aria-current="page"`,
			`method="get" action="/reports/sales"`, `name="from" value="2031-03-10"`,
			`href="/reports/sales?from=2031-03-10&amp;to=2031-03-10" data-shortcut="report.today" aria-current="true"`,
			`id="report-total" class="font-display font-bold text-3xl xl:text-4xl tabular whitespace-nowrap">$ 500,00`, `id="report-avg" class="font-display font-bold text-3xl xl:text-4xl tabular whitespace-nowrap">$ 250,00`,
			"5 unidades<br>1,25 kg", "Anuladas: 1 por $ 700,00", `<td class="px-3 py-2 text-right tabular whitespace-nowrap">$ 350,00</td>`,
			"70%", "Por hora", ">09:00</th>", ">10:00</th>", "Transferencia"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		if strings.Contains(html, `id="report-empty"`) {
			t.Error("unexpected empty notice")
		}
	})

	t.Run("sales: nothing sold", func(t *testing.T) {
		rep := &reports.Sales{Granularity: model.ByHour, Total: reports.Row{ByMethod: map[string]decimal.Decimal{}}}
		html := render(t, "report_sales.gohtml", map[string]any{"Filter": filter, "Report": rep, "Rows": salesRows("es", rep)})
		if !strings.Contains(html, "No hubo ventas en este período.") || strings.Contains(html, `id="report-rows"`) {
			t.Error("empty: missing the notice or has the table")
		}
	})

	t.Run("bad period", func(t *testing.T) {
		html := render(t, "report_sales.gohtml", map[string]any{"Filter": filter, "Error": "La fecha está mal."})
		if !strings.Contains(html, `id="report-error" role="alert"`) || strings.Contains(html, `id="report-total"`) {
			t.Error("error: missing the alert or has a report")
		}
	})

	t.Run("cash", func(t *testing.T) {
		ana := "Ana"
		closed := "2031-03-11T04:00:00Z"
		tally := &model.SessionTally{Session: &model.CashierSession{ID: 7, RegisterName: "Caja 1", OpeningAmount: dec("1000"),
			OpenedAt: "2031-03-10T11:00:00Z", ClosedAt: &closed, OpenedByName: &ana},
			Sales: 2, Total: dec("1710"), Voided: 1, VoidedTotal: dec("900"),
			Margin: model.Margin{Net: dec("1000"), Cost: dec("600"), NoCost: dec("500")}}
		rep := &reports.Cash{Sessions: []reports.Session{{SessionTally: tally}}, Open: 1, Sales: 2, Total: dec("1710"),
			Voided: 1, VoidedTotal: dec("900"), Difference: dec("-100"), Margin: tally.Margin}
		cashFilter := filter
		cashFilter.Action = "/reports/cash"
		html := render(t, "report_cash.gohtml", map[string]any{"Section": "reports_cash", "TitleKey": "report.cash_title",
			"Filter": cashFilter, "Report": rep, "Rows": []cashRow{{SessionTally: tally, Diff: dec("100"), Short: true}},
			"Diff": dec("100"), "Short": true})
		for _, s := range []string{"<title>ElmanPOS - Caja y margen</title>", `href="/reports/cash" aria-current="page"`,
			`data-session="7"`, "Caja 1 · 2031-03-10 08:00:00", "Abrió Ana · Cerró —", "Cambio inicial: $ 1.000,00", "2 ventas", "1 anulada: $ 900,00",
			`<span class="text-danger">Faltan $ 100,00</span>`, "$ 400,00", `<span class="block text-sm text-muted">40%</span>`,
			`text-danger">$ 100,00</p>`, "Falta plata en total",
			"$ 500,00 sin costo", `href="/cash/sessions/7"`, `id="report-open"`,
			"Se vendieron $ 500,00 de productos sin costo cargado"} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
	})

	t.Run("cash: no sessions", func(t *testing.T) {
		rep := &reports.Cash{}
		html := render(t, "report_cash.gohtml", map[string]any{"Filter": filter, "Report": rep, "Diff": decimal.Zero})
		for _, s := range []string{"No hay cajas cerradas en este período.", `text-stock">Justo</p>`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		for _, s := range []string{`id="report-open"`, `id="report-no-cost"`, `id="report-sessions-table"`} {
			if strings.Contains(html, s) {
				t.Errorf("unexpected %q", s)
			}
		}
	})
}
