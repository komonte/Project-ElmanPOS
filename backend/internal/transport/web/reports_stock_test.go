package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/reports"
)

func TestRenderStockReports(t *testing.T) {
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
	dec := decimal.RequireFromString
	supplier, max := int64(4), dec("24")
	name := "Arcor"

	t.Run("restock", func(t *testing.T) {
		rep := &reports.Restock{
			Below: []reports.RestockGroup{
				{SupplierID: &supplier, SupplierName: name, Items: []model.RestockItem{
					{ProductID: 1, SKU: "000001", Name: "OREO", SaleType: model.SaleTypeUnit, Quantity: dec("0"),
						ReorderPoint: dec("6"), MaxStock: &max, Sold: dec("30")},
					{ProductID: 3, SKU: "000003", Name: "MORTADELA", SaleType: model.SaleTypeWeight, Quantity: dec("1.25"),
						ReorderPoint: dec("2"), Sold: dec("0.5")},
				}},
				{Items: []model.RestockItem{{ProductID: 2, SKU: "000002", Name: "SODA", SaleType: model.SaleTypeUnit,
					Quantity: dec("1"), ReorderPoint: dec("3"), Sold: dec("12")}}},
			},
			RanOut: []model.RestockItem{{ProductID: 9, SKU: "000009", Name: "YERBA", SaleType: model.SaleTypeUnit,
				Quantity: dec("0"), Sold: dec("5")}},
		}
		html := render(t, "report_restock.gohtml", stockCan, map[string]any{"Section": "reports_restock",
			"TitleKey": "report.restock_title", "Report": rep, "SoldDays": 30, "Generated": "2026-11-26T18:00:00Z"})
		for _, s := range []string{"<title>ElmanPOS - Reposición</title>", `href="/reports/restock" aria-current="page"`,
			`onclick="window.print()"`, `<nav class="print:hidden`, "Armado el 2026-11-26 15:00:00",
			`data-supplier="4"`, ">Arcor</h2>", `data-supplier="none"`, ">Sin proveedor</h2>", "Vendido (30 días)",
			`<td class="px-3 py-2 text-right tabular whitespace-nowrap font-bold" data-suggested>24</td>`, // hasta el máximo
			`<td class="px-3 py-2 text-right tabular whitespace-nowrap font-bold" data-suggested>—</td>`,  // con stock de sobra
			"1,25 kg", `<td class="px-3 py-2 text-right tabular whitespace-nowrap font-bold" data-suggested>11</td>`,
			`id="report-ran-out"`, "YERBA", `<td class="px-3 py-2 text-right tabular whitespace-nowrap font-bold" data-suggested>5</td>`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		// El repositor no ve los reportes de plata en el menú.
		if strings.Contains(html, `href="/reports/sales"`) || strings.Contains(html, `id="report-empty"`) {
			t.Error("unexpected sales link or empty notice")
		}
	})

	t.Run("restock: nothing", func(t *testing.T) {
		html := render(t, "report_restock.gohtml", adminCan, map[string]any{"Report": &reports.Restock{}, "SoldDays": 30, "Generated": "2026-11-26T18:00:00Z"})
		if !strings.Contains(html, `id="report-empty"`) || strings.Contains(html, "<table") {
			t.Error("empty: missing notice or has a table")
		}
	})

	t.Run("expiring", func(t *testing.T) {
		batch := func(id int64, days int, active bool) reports.ExpiringRow {
			return reports.ExpiringRow{ExpiringBatch: model.ExpiringBatch{BatchID: id, BatchNumber: "L-1", ProductID: 7,
				SKU: "000007", Name: "YOGUR", ProductActive: active, SaleType: model.SaleTypeUnit, Expiry: "2026-11-20",
				Remaining: dec("3"), UnitCost: dec("100")}, DaysLeft: days}
		}
		rep := &reports.Expiring{Days: 30, Expired: []reports.ExpiringRow{batch(1, -6, true), batch(2, -1, false)},
			Soon:         []reports.ExpiringRow{batch(3, 0, true), batch(4, 1, true), batch(5, 12, true)},
			ExpiredValue: dec("600"), SoonValue: dec("900")}
		html := render(t, "report_expiring.gohtml", stockCan, map[string]any{"Section": "reports_expiring",
			"TitleKey": "report.expiring_title", "Report": rep, "Options": []dayOption{{7, false}, {30, true}},
			"Expired": expiringRows("es", rep.Expired), "Soon": expiringRows("es", rep.Soon)})
		for _, s := range []string{"<title>ElmanPOS - Por vencer</title>", `href="/reports/expiring?days=30" data-days="30" aria-current="true"`,
			`id="report-expired-value" class="font-display font-bold text-3xl xl:text-4xl tabular whitespace-nowrap text-danger">$ 600,00`,
			"Vence en los próximos 30 días", "2 lotes", "3 lotes", "20/11/2026", "venció hace 6 días", "venció ayer", "vence hoy",
			"vence mañana", "en 12 días", "producto desactivado", "$ 300,00",
			`href="/adjustments?product_id=7&amp;batch_id=1&amp;reason=EXPIRATION" data-write-off`} {
			if !strings.Contains(html, s) {
				t.Errorf("missing %q", s)
			}
		}
		// El producto desactivado no se puede dar de baja (la salida lo rechazaría).
		if strings.Contains(html, "batch_id=2&") {
			t.Error("write-off offered for an inactive product")
		}
	})

	t.Run("expiring: nothing", func(t *testing.T) {
		html := render(t, "report_expiring.gohtml", stockCan, map[string]any{"Report": &reports.Expiring{Days: 7},
			"Options": []dayOption{{7, true}}})
		if !strings.Contains(html, "No hay lotes vencidos ni que venzan en los próximos 7 días.") || strings.Contains(html, "<table") {
			t.Error("empty: missing notice or has a table")
		}
	})
}

func TestExpiringRowsEnglishDate(t *testing.T) {
	rows := expiringRows("en", []reports.ExpiringRow{{ExpiringBatch: model.ExpiringBatch{Expiry: "2026-11-20"}, DaysLeft: -3}})
	if rows[0].Date != "2026-11-20" || rows[0].Ago != 3 {
		t.Errorf("got %+v", rows[0])
	}
}
