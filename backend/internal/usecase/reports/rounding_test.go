package reports

import (
	"testing"
	"time"
)

// Redondeo en efectivo (0024): por mes, cuántas ventas y cuánto. Las
// anuladas no cuentan.
func TestRounding(t *testing.T) {
	f := newFixture(t)
	s := f.session(t, f.at(3, 1, 8, 0), "0")
	rounded := func(at time.Time, voided bool, discount string) {
		f.sale(t, s, at, voided, []item{{f.unit, "1", "1245", "0", "21"}}, map[string]string{"CASH": "1200"})
		f.exec(t, `UPDATE sales SET discount_amount = $2, total = subtotal - $2
			WHERE id = (SELECT id FROM sales WHERE cashier_session_id = $1 ORDER BY id DESC LIMIT 1);`, s, discount)
	}
	rounded(f.at(3, 2, 10, 0), false, "45")
	rounded(f.at(3, 20, 10, 0), false, "45")
	rounded(f.at(3, 21, 10, 0), true, "45") // anulada
	rounded(f.at(5, 31, 23, 30), false, "10")
	f.sale(t, s, f.at(5, 10, 10, 0), false, []item{{f.unit, "1", "500", "0", "21"}}, map[string]string{"CASH": "500"})
	f.sale(t, s, f.at(5, 11, 10, 0), false, []item{{f.unit, "1", "500", "0", "21"}}, map[string]string{"DEBIT_CARD": "500"})

	r, err := f.svc.Rounding(f.ctx, f.at(6, 15, 12, 0), f.ba)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Months) != RoundingMonths {
		t.Fatalf("months = %d, want %d", len(r.Months), RoundingMonths)
	}
	if !r.Months[0].Start.Equal(f.at(6, 1, 0, 0)) || !r.Months[11].Start.Equal(time.Date(2030, 7, 1, 0, 0, 0, 0, f.ba)) {
		t.Errorf("first %v, last %v: want June 2031 down to July 2030", r.Months[0].Start, r.Months[11].Start)
	}
	byMonth := map[time.Month]RoundingMonth{}
	for _, m := range r.Months {
		if m.Start.Year() == 2031 {
			byMonth[m.Start.Month()] = m
		}
	}
	if m := byMonth[time.March]; m.Rounded != 2 || !m.Amount.Equal(d("90")) || m.CashSales != 2 {
		t.Errorf("March: %+v", m)
	}
	// 31/5 a las 23:30 en Buenos Aires es 1/6 en UTC: va en mayo.
	if m := byMonth[time.May]; m.Rounded != 1 || !m.Amount.Equal(d("10")) || m.CashSales != 2 {
		t.Errorf("May: %+v", m)
	}
	if m := byMonth[time.June]; m.Rounded != 0 || !m.Amount.IsZero() {
		t.Errorf("June: %+v", m)
	}
	if r.Total.Rounded != 3 || !r.Total.Amount.Equal(d("100")) || r.Total.CashSales != 4 {
		t.Errorf("total: %+v", r.Total)
	}
}
