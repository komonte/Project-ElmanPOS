package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/i18n"
	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/reports"
)

// Reportes de ventas (R-06) y de caja y margen (R-07): solo el admin
// (model.PermReports). El período va en la dirección (?from=&to=), así un
// reporte se puede guardar o mandar como link.

// WithReports le da al handler los reportes.
func (h *WebHandler) WithReports(r *reports.Service) *WebHandler {
	h.reports = r
	return h
}

// reportNow es el reloj de los reportes ("hoy"). Los tests lo cambian.
var reportNow = time.Now

// shortcut es un atajo del filtro: Hoy, Ayer, Últimos 7 días...
type shortcut struct {
	Key      string // i18n
	From, To string // 2026-11-26
	Active   bool
}

// periodFilter es el filtro de fechas de un reporte.
type periodFilter struct {
	Action    string // a dónde va el formulario
	From, To  string // lo escrito (o el período, si no hubo error)
	Shortcuts []shortcut
}

// shortcuts son los atajos del filtro para "hoy" (today: medianoche en el
// local). Active marca el que coincide con el período.
func shortcuts(today time.Time, p *model.Period) []shortcut {
	day := func(t time.Time) string { return t.Format(model.PeriodDateLayout) }
	first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
	list := []shortcut{
		{Key: "report.today", From: day(today), To: day(today)},
		{Key: "report.yesterday", From: day(today.AddDate(0, 0, -1)), To: day(today.AddDate(0, 0, -1))},
		{Key: "report.last_7_days", From: day(today.AddDate(0, 0, -6)), To: day(today)},
		{Key: "report.this_month", From: day(first), To: day(today)},
		{Key: "report.last_month", From: day(first.AddDate(0, -1, 0)), To: day(first.AddDate(0, 0, -1))},
	}
	if p != nil {
		for i := range list {
			list[i].Active = list[i].From == day(p.From) && list[i].To == day(p.To)
		}
	}
	return list
}

// reportPeriod lee el período de la dirección. Si está mal, devuelve el
// mensaje y el filtro con lo que se escribió.
func (h *WebHandler) reportPeriod(r *http.Request, action string) (*model.Period, periodFilter, string) {
	loc := storeLocation()
	now := reportNow().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	p, err := model.ParsePeriod(from, to, loc, now)
	if err != nil {
		return nil, periodFilter{Action: action, From: from, To: to, Shortcuts: shortcuts(today, nil)}, h.errMsg(r, err)
	}
	return &p, periodFilter{Action: action, From: p.From.Format(model.PeriodDateLayout),
		To: p.To.Format(model.PeriodDateLayout), Shortcuts: shortcuts(today, &p)}, ""
}

// bucketLabel es cómo se nombra un tramo: "09:00", "lun 10/03/2031" o
// "marzo 2031".
func bucketLabel(lang string, g model.Granularity, t time.Time) string {
	switch g {
	case model.ByHour:
		return t.Format("15:04")
	case model.ByMonth:
		return fmt.Sprintf("%s %d", i18n.T(lang, fmt.Sprintf("month.%d", int(t.Month()))), t.Year())
	default:
		layout := "02/01/2006"
		if lang != "es" {
			layout = "2006-01-02"
		}
		return i18n.T(lang, fmt.Sprintf("weekday.%d", int(t.Weekday()))) + " " + t.Format(layout)
	}
}

// salesRow es una fila de la tabla de ventas: su nombre y lo cobrado con
// cada medio, en el orden de las columnas.
type salesRow struct {
	Label string
	reports.Row
	Methods []decimal.Decimal
}

func salesRows(lang string, rep *reports.Sales) []salesRow {
	rows := make([]salesRow, len(rep.Rows))
	for i, r := range rep.Rows {
		rows[i] = salesRow{Label: bucketLabel(lang, rep.Granularity, r.Start), Row: r, Methods: byColumn(rep, r)}
	}
	return rows
}

func byColumn(rep *reports.Sales, r reports.Row) []decimal.Decimal {
	out := make([]decimal.Decimal, len(rep.Methods))
	for i, m := range rep.Methods {
		out[i] = r.ByMethod[m.Method]
	}
	return out
}

// ReportsSales handles GET /reports/sales
func (h *WebHandler) ReportsSales(w http.ResponseWriter, r *http.Request) {
	p, filter, errMsg := h.reportPeriod(r, "/reports/sales")
	data := map[string]any{"Section": "reports_sales", "TitleKey": "report.sales_title", "Filter": filter, "Error": errMsg}
	if p != nil {
		rep, err := h.reports.Sales(r.Context(), *p)
		if err != nil {
			http.Error(w, h.errMsg(r, err), http.StatusInternalServerError)
			return
		}
		lang := transport.GetLocale(r.Context())
		data["Report"] = rep
		data["Rows"] = salesRows(lang, rep)
		data["TotalMethods"] = byColumn(rep, rep.Total)
	}
	h.render(w, r, "report_sales.gohtml", data)
}

// cashRow es una sesión en la tabla de caja: la diferencia sin signo y si
// falta, como en el resumen del cierre.
type cashRow struct {
	*model.SessionTally
	Diff  decimal.Decimal
	Short bool
}

// ReportsCash handles GET /reports/cash
func (h *WebHandler) ReportsCash(w http.ResponseWriter, r *http.Request) {
	p, filter, errMsg := h.reportPeriod(r, "/reports/cash")
	data := map[string]any{"Section": "reports_cash", "TitleKey": "report.cash_title", "Filter": filter, "Error": errMsg}
	if p != nil {
		rep, err := h.reports.Cash(r.Context(), *p)
		if err != nil {
			http.Error(w, h.errMsg(r, err), http.StatusInternalServerError)
			return
		}
		rows := make([]cashRow, len(rep.Sessions))
		for i, s := range rep.Sessions {
			d := s.Difference()
			rows[i] = cashRow{SessionTally: s.SessionTally, Diff: d.Abs(), Short: d.IsNegative()}
		}
		data["Report"] = rep
		data["Rows"] = rows
		data["Diff"] = rep.Difference.Abs()
		data["Short"] = rep.Difference.IsNegative()
	}
	h.render(w, r, "report_cash.gohtml", data)
}
