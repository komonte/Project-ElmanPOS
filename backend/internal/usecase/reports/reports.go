// Package reports arma los reportes de ventas (R-06) y de caja y margen
// (R-07). Solo lee: los reportes se calculan, nunca se guardan.
//
// Decisiones (2026-11-26):
//   - Solo ventas COMPLETED. Las anuladas van aparte, contadas por cuándo se
//     hicieron.
//   - Por hora, se muestran las horas desde la primera hasta la última con
//     ventas (las del medio, aunque estén vacías). Por día o por mes, todos
//     los tramos del período: un día sin ventas también dice algo.
//   - Los medios de pago son los que tuvieron pagos en el período, en el
//     orden de model.SaleMethods (con la cuenta corriente: lo fiado).
//   - Caja: solo las sesiones cerradas, por el día en que se abrieron. Una
//     abierta se cuenta aparte: mostrar lo esperado antes de contar
//     arruinaría el cierre ciego (F-12). Su arqueo sale de
//     model.NewCloseSummary, el mismo del resumen de cierre.
package reports

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

type Service struct {
	db postgres.DBTX
}

func New(db postgres.DBTX) *Service {
	return &Service{db: db}
}

// Row es una fila del reporte de ventas: un tramo, o el total.
type Row struct {
	// Start es cuándo empieza el tramo, en la hora del local (cero en el
	// total).
	Start time.Time       `json:"start"`
	Count int             `json:"count"`
	Total decimal.Decimal `json:"total"`
	Units decimal.Decimal `json:"units"`
	Kg    decimal.Decimal `json:"kg"`
	// ByMethod es lo cobrado con cada medio.
	ByMethod map[string]decimal.Decimal `json:"by_method"`
}

// AvgTicket es el total dividido las ventas, en centavos. 0 sin ventas.
func (r Row) AvgTicket() decimal.Decimal {
	if r.Count == 0 {
		return decimal.Zero
	}
	return r.Total.Div(decimal.NewFromInt(int64(r.Count))).Round(2)
}

// Method es lo cobrado con un medio en todo el período.
type Method struct {
	Method string          `json:"method"`
	Count  int             `json:"count"`
	Amount decimal.Decimal `json:"amount"`
	// Share es el porcentaje del total vendido, con un decimal.
	Share decimal.Decimal `json:"share"`
}

// Sales es el reporte de ventas por período y medio de pago (R-06).
type Sales struct {
	Period      model.Period      `json:"-"`
	Granularity model.Granularity `json:"granularity"`
	Rows        []Row             `json:"rows"`
	Total       Row               `json:"total"`
	Methods     []Method          `json:"methods"`
	Voided      int               `json:"voided"`
	VoidedTotal decimal.Decimal   `json:"voided_total"`
}

// Sales arma el reporte de ventas del período.
func (s *Service) Sales(ctx context.Context, p model.Period) (*Sales, error) {
	reports := postgres.NewReportStore(s.db)
	buckets := p.Buckets()
	sales, err := reports.SalesByBucket(ctx, buckets, p.End())
	if err != nil {
		return nil, err
	}
	pays, err := reports.PaymentsByBucket(ctx, buckets, p.End())
	if err != nil {
		return nil, err
	}
	out := &Sales{Period: p, Granularity: p.Granularity(), Total: newRow(time.Time{})}
	if out.Voided, out.VoidedTotal, err = reports.Voided(ctx, p.Start(), p.End()); err != nil {
		return nil, err
	}

	rows := make([]Row, len(buckets))
	for i, b := range buckets {
		rows[i] = newRow(b)
	}
	// Un tramo fuera de rango no debería pasar (la consulta filtra por el
	// período); si pasa, mejor dejarlo afuera que romper la página.
	in := func(bucket int) bool { return bucket >= 0 && bucket < len(rows) }
	for _, t := range sales {
		if !in(t.Bucket) {
			continue
		}
		r := &rows[t.Bucket]
		r.Count, r.Total, r.Units, r.Kg = t.Count, t.Total, t.Units, t.Kg
		out.Total.Count += t.Count
		out.Total.Total = out.Total.Total.Add(t.Total)
		out.Total.Units = out.Total.Units.Add(t.Units)
		out.Total.Kg = out.Total.Kg.Add(t.Kg)
	}
	methods := map[string]*Method{}
	for _, t := range pays {
		if !in(t.Bucket) {
			continue
		}
		rows[t.Bucket].ByMethod[t.Method] = t.Amount
		out.Total.ByMethod[t.Method] = out.Total.ByMethod[t.Method].Add(t.Amount)
		m := methods[t.Method]
		if m == nil {
			m = &Method{Method: t.Method}
			methods[t.Method] = m
		}
		m.Count += t.Count
		m.Amount = m.Amount.Add(t.Amount)
	}
	for _, name := range model.SaleMethods {
		if m := methods[name]; m != nil {
			if !out.Total.Total.IsZero() {
				m.Share = m.Amount.Mul(decimal.NewFromInt(100)).Div(out.Total.Total).Round(1)
			}
			out.Methods = append(out.Methods, *m)
		}
	}

	if out.Granularity == model.ByHour {
		rows = trimEmpty(rows)
	}
	out.Rows = rows
	return out, nil
}

func newRow(start time.Time) Row {
	return Row{Start: start, ByMethod: map[string]decimal.Decimal{}}
}

// trimEmpty saca las filas sin ventas del principio y del final.
func trimEmpty(rows []Row) []Row {
	first, last := -1, -1
	for i, r := range rows {
		if r.Count > 0 {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	if first < 0 {
		return nil
	}
	return rows[first : last+1]
}

// Session es una sesión cerrada en el reporte de caja, con su arqueo.
type Session struct {
	*model.SessionTally
	Close *model.CloseSummary `json:"close"`
}

// Difference es la diferencia del arqueo, sumando todos los medios:
// negativa es faltante, positiva sobrante.
func (s Session) Difference() decimal.Decimal {
	sum := decimal.Zero
	for _, l := range s.Close.Lines {
		sum = sum.Add(l.Difference())
	}
	return sum
}

// Cash es el reporte de caja y margen (R-07).
type Cash struct {
	Period   model.Period `json:"-"`
	Sessions []Session    `json:"sessions"`
	// Open son las sesiones del período que siguen abiertas: no están en la
	// lista hasta que se cierren.
	Open        int             `json:"open"`
	Sales       int             `json:"sales"`
	Total       decimal.Decimal `json:"total"`
	Voided      int             `json:"voided"`
	VoidedTotal decimal.Decimal `json:"voided_total"`
	Difference  decimal.Decimal `json:"difference"`
	Margin      model.Margin    `json:"margin"`
}

// Cash arma el reporte de caja y margen del período.
func (s *Service) Cash(ctx context.Context, p model.Period) (*Cash, error) {
	reports := postgres.NewReportStore(s.db)
	tallies, err := reports.ClosedSessions(ctx, p.Start(), p.End())
	if err != nil {
		return nil, err
	}
	out := &Cash{Period: p, Sessions: make([]Session, 0, len(tallies))}
	if out.Open, err = reports.OpenSessions(ctx, p.Start(), p.End()); err != nil {
		return nil, err
	}
	for _, t := range tallies {
		sum, err := model.NewCloseSummary(t.Session.OpeningAmount, t.Paid, t.Declared)
		if err != nil {
			return nil, err
		}
		row := Session{SessionTally: t, Close: sum}
		out.Sessions = append(out.Sessions, row)
		out.Sales += t.Sales
		out.Total = out.Total.Add(t.Total)
		out.Voided += t.Voided
		out.VoidedTotal = out.VoidedTotal.Add(t.VoidedTotal)
		out.Difference = out.Difference.Add(row.Difference())
		out.Margin = out.Margin.Add(t.Margin)
	}
	return out, nil
}
