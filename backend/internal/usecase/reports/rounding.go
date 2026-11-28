package reports

import (
	"context"
	"time"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// RoundingMonths es cuántos meses muestra el reporte de redondeo: el actual
// y los 11 anteriores.
const RoundingMonths = 12

// RoundingMonth es una fila del reporte de redondeo en efectivo: un mes, o
// el total.
type RoundingMonth struct {
	// Start es el primer día del mes, en la hora del local (cero en el
	// total).
	Start     time.Time       `json:"start"`
	Rounded   int             `json:"rounded"`
	Amount    decimal.Decimal `json:"amount"`
	CashSales int             `json:"cash_sales"`
}

// Rounding es el reporte mensual de redondeo en efectivo (2026-11-28,
// migración 0024): cuánto se dejó de cobrar por redondear para abajo.
type Rounding struct {
	Months []RoundingMonth `json:"months"` // el más nuevo primero
	Total  RoundingMonth   `json:"total"`
}

// Rounding arma el reporte de los últimos RoundingMonths meses hasta el de
// now (todos, aunque no tengan ventas), en la zona loc.
func (s *Service) Rounding(ctx context.Context, now time.Time, loc *time.Location) (*Rounding, error) {
	now = now.In(loc)
	first := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, -(RoundingMonths - 1), 0)
	buckets := make([]time.Time, RoundingMonths)
	for i := range buckets {
		buckets[i] = first.AddDate(0, i, 0)
	}
	end := first.AddDate(0, RoundingMonths, 0)
	tallies, err := postgres.NewReportStore(s.db).CashRoundingByBucket(ctx, buckets, end)
	if err != nil {
		return nil, err
	}
	months := make([]RoundingMonth, RoundingMonths)
	for i, b := range buckets {
		months[i] = RoundingMonth{Start: b}
	}
	out := &Rounding{}
	for _, t := range tallies {
		if t.Bucket < 0 || t.Bucket >= len(months) {
			continue
		}
		m := &months[t.Bucket]
		m.Rounded, m.Amount, m.CashSales = t.Rounded, t.Amount, t.CashSales
		out.Total.Rounded += t.Rounded
		out.Total.Amount = out.Total.Amount.Add(t.Amount)
		out.Total.CashSales += t.CashSales
	}
	for i := len(months) - 1; i >= 0; i-- {
		out.Months = append(out.Months, months[i])
	}
	return out, nil
}
