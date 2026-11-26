package model

import (
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestParsePeriod(t *testing.T) {
	ba, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		t.Fatal(err)
	}
	// 26/11 a las 01:30 en Buenos Aires son las 04:30 en UTC.
	now := time.Date(2026, 11, 26, 4, 30, 0, 0, time.UTC)
	day := func(s string) time.Time {
		d, _ := time.ParseInLocation(PeriodDateLayout, s, ba)
		return d
	}

	cases := []struct {
		name, from, to string
		wantFrom       string
		wantTo         string
		days           int
		gran           Granularity
	}{
		{"empty is today in the store", "", "", "2026-11-26", "2026-11-26", 1, ByHour},
		{"only from", "2026-11-20", "", "2026-11-20", "2026-11-20", 1, ByHour},
		{"only to", "", "2026-11-21", "2026-11-21", "2026-11-21", 1, ByHour},
		{"a week", "2026-11-20", "2026-11-26", "2026-11-20", "2026-11-26", 7, ByDay},
		{"62 days by day", "2026-09-26", "2026-11-26", "2026-09-26", "2026-11-26", 62, ByDay},
		{"63 days by month", "2026-09-25", "2026-11-26", "2026-09-25", "2026-11-26", 63, ByMonth},
		{"a leap year", "2028-01-01", "2028-12-31", "2028-01-01", "2028-12-31", 366, ByMonth},
	}
	for _, c := range cases {
		p, err := ParsePeriod(c.from, c.to, ba, now)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !p.From.Equal(day(c.wantFrom)) || !p.To.Equal(day(c.wantTo)) || p.Days() != c.days || p.Granularity() != c.gran {
			t.Errorf("%s: got %v..%v, %d days, %s", c.name, p.From, p.To, p.Days(), p.Granularity())
		}
		if !p.End().Equal(day(c.wantTo).AddDate(0, 0, 1)) {
			t.Errorf("%s: end %v", c.name, p.End())
		}
	}

	for _, c := range []struct {
		from, to string
		want     error
	}{
		{"26/11/2026", "", ErrInvalidPeriodDate},
		{"2026-11-26", "mañana", ErrInvalidPeriodDate},
		{"2026-02-30", "", ErrInvalidPeriodDate},
		{"2026-11-26", "2026-11-25", ErrPeriodReversed},
		{"2027-01-01", "2028-01-02", ErrPeriodTooLong},
	} {
		if _, err := ParsePeriod(c.from, c.to, ba, now); !errors.Is(err, c.want) {
			t.Errorf("%q..%q: got %v, want %v", c.from, c.to, err, c.want)
		}
	}
}

func TestPeriodBuckets(t *testing.T) {
	ba, _ := time.LoadLocation("America/Argentina/Buenos_Aires")
	now := time.Now()
	starts := func(t *testing.T, from, to, layout string) []string {
		t.Helper()
		p, err := ParsePeriod(from, to, ba, now)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, b := range p.Buckets() {
			out = append(out, b.Format(layout))
		}
		return out
	}

	hours := starts(t, "2026-11-26", "2026-11-26", "02 15:04 -07")
	if len(hours) != 24 || hours[0] != "26 00:00 -03" || hours[23] != "26 23:00 -03" {
		t.Errorf("hours: got %v", hours)
	}
	days := starts(t, "2026-11-29", "2026-12-02", "01-02")
	if want := []string{"11-29", "11-30", "12-01", "12-02"}; !equal(days, want) {
		t.Errorf("days: got %v", days)
	}
	// Por mes: el primero empieza en "desde" aunque no sea el 1.
	months := starts(t, "2026-01-15", "2026-04-10", "2006-01-02")
	if want := []string{"2026-01-15", "2026-02-01", "2026-03-01", "2026-04-01"}; !equal(months, want) {
		t.Errorf("months: got %v", months)
	}

	// Con cambio de hora (Nueva York, 8/3/2026: a las 2 pasa a las 3), el
	// día tiene 23 tramos y ninguno se repite.
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no tzdata")
	}
	p, _ := ParsePeriod("2026-03-08", "", ny, now)
	b := p.Buckets()
	if len(b) != 23 || !b[len(b)-1].Before(p.End()) {
		t.Errorf("DST day: %d buckets, last %v", len(b), b[len(b)-1])
	}
	for i := 1; i < len(b); i++ {
		if !b[i].After(b[i-1]) {
			t.Errorf("DST day: bucket %d (%v) not after %v", i, b[i], b[i-1])
		}
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMargin(t *testing.T) {
	d := decimal.RequireFromString
	m := Margin{Net: d("1000"), Cost: d("700"), NoCost: d("50")}
	if !m.Amount().Equal(d("300")) || !m.Percent().Equal(d("30")) {
		t.Errorf("margin: %v, %v%%", m.Amount(), m.Percent())
	}
	sum := m.Add(Margin{Net: d("200"), Cost: d("190"), NoCost: d("10")})
	if !sum.Net.Equal(d("1200")) || !sum.Cost.Equal(d("890")) || !sum.NoCost.Equal(d("60")) ||
		!sum.Percent().Equal(d("25.8")) {
		t.Errorf("add: %+v, %v%%", sum, sum.Percent())
	}
	if !(Margin{}).Percent().IsZero() {
		t.Error("no sales: 0%")
	}
}
