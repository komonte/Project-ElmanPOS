package web

import (
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/reports"
)

// Reportes de stock (R-01, R-02): el admin y el repositor (permiso stock).

// expiryDayOptions son los días que se pueden mirar en el reporte de
// vencimientos, además de los configurados.
var expiryDayOptions = []int{7, 15, 30, 60}

// ReportsRestock handles GET /reports/restock
func (h *WebHandler) ReportsRestock(w http.ResponseWriter, r *http.Request) {
	now := reportNow()
	rep, err := h.reports.Restock(r.Context(), now)
	if err != nil {
		http.Error(w, h.errMsg(r, err), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "report_restock.gohtml", map[string]any{
		"Section": "reports_restock", "TitleKey": "report.restock_title", "Report": rep,
		"SoldDays": reports.SoldDays, "Generated": now.UTC().Format(time.RFC3339),
	})
}

// dayOption es un botón de días del reporte de vencimientos.
type dayOption struct {
	Days   int
	Active bool
}

// expiringRow es un lote en la tabla, con la fecha como se lee y, si ya
// venció, hace cuántos días.
type expiringRow struct {
	reports.ExpiringRow
	Date string
	Ago  int
}

func expiringRows(lang string, rows []reports.ExpiringRow) []expiringRow {
	layout := "02/01/2006"
	if lang != "es" {
		layout = time.DateOnly
	}
	out := make([]expiringRow, len(rows))
	for i, row := range rows {
		date := row.Expiry
		if t, err := time.Parse(time.DateOnly, row.Expiry); err == nil {
			date = t.Format(layout)
		}
		out[i] = expiringRow{ExpiringRow: row, Date: date, Ago: -row.DaysLeft}
	}
	return out
}

// ReportsExpiring handles GET /reports/expiring. ?days= cambia cuántos días
// mira (solo los de los botones); sin eso, los configurados.
func (h *WebHandler) ReportsExpiring(w http.ResponseWriter, r *http.Request) {
	configured := h.reports.ExpiryDays(r.Context())
	days := configured
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && (d == configured || slices.Contains(expiryDayOptions, d)) {
		days = d
	}
	rep, err := h.reports.Expiring(r.Context(), reportNow().In(storeLocation()), days)
	if err != nil {
		http.Error(w, h.errMsg(r, err), http.StatusInternalServerError)
		return
	}
	choices := slices.Clone(expiryDayOptions)
	if !slices.Contains(choices, configured) {
		choices = append(choices, configured)
		slices.Sort(choices)
	}
	options := make([]dayOption, len(choices))
	for i, d := range choices {
		options[i] = dayOption{Days: d, Active: d == days}
	}
	lang := transport.GetLocale(r.Context())
	h.render(w, r, "report_expiring.gohtml", map[string]any{
		"Section": "reports_expiring", "TitleKey": "report.expiring_title", "Report": rep, "Options": options,
		"Expired": expiringRows(lang, rep.Expired), "Soon": expiringRows(lang, rep.Soon),
	})
}

// ReportsUnstocked handles GET /reports/unstocked: los productos vendidos sin
// stock (modo arranque) que hay que contar.
func (h *WebHandler) ReportsUnstocked(w http.ResponseWriter, r *http.Request) {
	items, err := h.reports.Unstocked(r.Context())
	if err != nil {
		http.Error(w, h.errMsg(r, err), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "report_unstocked.gohtml", map[string]any{
		"Section": "reports_unstocked", "TitleKey": "report.unstocked_title", "Items": items,
	})
}
