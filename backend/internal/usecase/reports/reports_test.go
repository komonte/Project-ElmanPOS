package reports

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

// Las ventas de prueba son de 2031: así no se mezclan con las que ya haya
// en la base.

var d = decimal.RequireFromString

type fixture struct {
	ctx      context.Context
	tx       *sql.Tx
	svc      *Service
	ba       *time.Location
	unit     int64 // producto por unidad, IVA 21%
	weight   int64 // por peso
	register int64
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; run `make test` with the DB up")
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin tx (is the DB up?): %v", err)
	}
	t.Cleanup(func() { tx.Rollback() })
	ba, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{ctx: context.Background(), tx: tx, svc: New(tx), ba: ba}
	tax := f.id(t, `INSERT INTO taxes (name, rate) VALUES ('IVA test reports', 21) RETURNING id;`)
	cat := f.id(t, `INSERT INTO categories (name, tax_id) VALUES ('Categoria test reports', $1) RETURNING id;`, tax)
	f.unit = f.id(t, `INSERT INTO products (sku, name, category_id) VALUES ('TEST-RPT-U', 'UNIDAD TEST', $1) RETURNING id;`, cat)
	f.weight = f.id(t, `INSERT INTO products (sku, name, category_id, sale_type, measure_unit)
		VALUES ('TEST-RPT-W', 'PESO TEST', $1, 'WEIGHT', 'KILOGRAM') RETURNING id;`, cat)
	store := f.id(t, `INSERT INTO stores (name) VALUES ('Local test reports') RETURNING id;`)
	f.register = f.id(t, `INSERT INTO cash_registers (store_id, name) VALUES ($1, 'Caja test') RETURNING id;`, store)
	return f
}

func (f *fixture) id(t *testing.T, q string, args ...any) int64 {
	t.Helper()
	var id int64
	if err := f.tx.QueryRowContext(f.ctx, q, args...).Scan(&id); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return id
}

func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.tx.ExecContext(f.ctx, q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func (f *fixture) at(month, day, hour, min int) time.Time {
	return time.Date(2031, time.Month(month), day, hour, min, 0, 0, f.ba)
}

// session abre una sesión en la caja del fixture a esa hora, con cambio.
func (f *fixture) session(t *testing.T, opened time.Time, opening string) int64 {
	t.Helper()
	return f.id(t, `INSERT INTO cashier_sessions (cash_register_id, opening_amount, opened_at) VALUES ($1, $2, $3) RETURNING id;`,
		f.register, opening, opened)
}

// close cierra la sesión con lo contado por medio (los que faltan, en 0).
func (f *fixture) close(t *testing.T, id int64, at time.Time, declared map[string]string) {
	t.Helper()
	f.exec(t, `UPDATE cashier_sessions SET status = 'CLOSED', closed_at = $2 WHERE id = $1;`, id, at)
	for _, m := range model.PaymentMethods {
		amount := declared[m]
		if amount == "" {
			amount = "0"
		}
		f.exec(t, `INSERT INTO cash_audits (cashier_session_id, method, declared_amount) VALUES ($1, $2, $3);`, id, m, amount)
	}
}

type item struct {
	product                int64
	qty, price, cost, rate string
}

// sale guarda una venta de un renglón por producto, pagada como se dice.
func (f *fixture) sale(t *testing.T, session int64, at time.Time, voided bool, items []item, pays map[string]string) {
	t.Helper()
	total := decimal.Zero
	for _, it := range items {
		total = total.Add(d(it.qty).Mul(d(it.price)).Round(2))
	}
	var n int
	if err := f.tx.QueryRowContext(f.ctx, `SELECT COALESCE(MAX(sale_number), 0) + 1 FROM sales WHERE cashier_session_id = $1;`,
		session).Scan(&n); err != nil {
		t.Fatal(err)
	}
	status, voidedAt, reason := "COMPLETED", (*time.Time)(nil), (*string)(nil)
	if voided {
		r := "test"
		status, voidedAt, reason = "VOIDED", &at, &r
	}
	id := f.id(t, `INSERT INTO sales (cashier_session_id, sale_number, status, subtotal, tax_amount, total,
		created_at, voided_at, void_reason) VALUES ($1, $2, $3, $4, 0, $4, $5, $6, $7) RETURNING id;`,
		session, n, status, total, at, voidedAt, reason)
	for _, it := range items {
		f.exec(t, `INSERT INTO sale_items (sale_id, product_id, quantity, unit_price, unit_cost, tax_rate)
			VALUES ($1, $2, $3, $4, $5, $6);`, id, it.product, it.qty, it.price, it.cost, it.rate)
	}
	for m, amount := range pays {
		f.exec(t, `INSERT INTO payments (sale_id, method, amount) VALUES ($1, $2, $3);`, id, m, amount)
	}
}

func (f *fixture) period(t *testing.T, from, to string) model.Period {
	t.Helper()
	p, err := model.ParsePeriod(from, to, f.ba, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSalesByHour(t *testing.T) {
	f := newFixture(t)
	s := f.session(t, f.at(3, 10, 8, 0), "0")
	f.sale(t, s, f.at(3, 10, 9, 10), false, []item{{f.unit, "3", "100", "60", "21"}}, map[string]string{"CASH": "300"})
	f.sale(t, s, f.at(3, 10, 9, 40), false, []item{{f.unit, "2", "100", "60", "21"}},
		map[string]string{"CASH": "50", "TRANSFER": "150"})
	f.sale(t, s, f.at(3, 10, 12, 0), false, []item{{f.weight, "0.750", "2000", "1000", "21"}}, map[string]string{"QR": "1500"})
	f.sale(t, s, f.at(3, 10, 11, 0), true, []item{{f.unit, "1", "700", "60", "21"}}, map[string]string{"CASH": "700"})

	r, err := f.svc.Sales(f.ctx, f.period(t, "2031-03-10", ""))
	if err != nil {
		t.Fatal(err)
	}
	if r.Granularity != model.ByHour {
		t.Errorf("granularity: %s", r.Granularity)
	}
	// De 9 a 12: las 10 y las 11 quedan en cero (la anulada no cuenta).
	if len(r.Rows) != 4 || r.Rows[0].Start.Hour() != 9 || r.Rows[3].Start.Hour() != 12 ||
		r.Rows[1].Count != 0 || r.Rows[2].Count != 0 {
		t.Fatalf("rows: %+v", r.Rows)
	}
	nine := r.Rows[0]
	if nine.Count != 2 || !nine.Total.Equal(d("500")) || !nine.Units.Equal(d("5")) || !nine.ByMethod["CASH"].Equal(d("350")) ||
		!nine.ByMethod["TRANSFER"].Equal(d("150")) || !nine.AvgTicket().Equal(d("250")) {
		t.Errorf("9 h: %+v, avg %v", nine, nine.AvgTicket())
	}
	tot := r.Total
	if tot.Count != 3 || !tot.Total.Equal(d("2000")) || !tot.Units.Equal(d("5")) || !tot.Kg.Equal(d("0.75")) ||
		!tot.AvgTicket().Equal(d("666.67")) || !tot.ByMethod["QR"].Equal(d("1500")) {
		t.Errorf("total: %+v, avg %v", tot, tot.AvgTicket())
	}
	// Medios en el orden de siempre, solo los que se usaron.
	want := []Method{{"CASH", 2, d("350"), d("17.5")}, {"TRANSFER", 1, d("150"), d("7.5")}, {"QR", 1, d("1500"), d("75")}}
	if len(r.Methods) != len(want) {
		t.Fatalf("methods: %+v", r.Methods)
	}
	for i, m := range want {
		got := r.Methods[i]
		if got.Method != m.Method || got.Count != m.Count || !got.Amount.Equal(m.Amount) || !got.Share.Equal(m.Share) {
			t.Errorf("method %d: got %+v, want %+v", i, got, m)
		}
	}
	if r.Voided != 1 || !r.VoidedTotal.Equal(d("700")) {
		t.Errorf("voided: %d %v", r.Voided, r.VoidedTotal)
	}
}

func TestSalesByDayAndMonth(t *testing.T) {
	f := newFixture(t)
	s := f.session(t, f.at(3, 1, 8, 0), "0")
	f.sale(t, s, f.at(3, 2, 10, 0), false, []item{{f.unit, "1", "100", "60", "21"}}, map[string]string{"CASH": "100"})
	f.sale(t, s, f.at(3, 4, 23, 59), false, []item{{f.unit, "1", "200", "60", "21"}}, map[string]string{"CASH": "200"})
	f.sale(t, s, f.at(5, 20, 10, 0), false, []item{{f.unit, "1", "400", "60", "21"}}, map[string]string{"DEBIT_CARD": "400"})

	days, err := f.svc.Sales(f.ctx, f.period(t, "2031-03-01", "2031-03-05"))
	if err != nil {
		t.Fatal(err)
	}
	// Todos los días, también los vacíos del principio y del final.
	if days.Granularity != model.ByDay || len(days.Rows) != 5 || days.Rows[0].Count != 0 || days.Rows[1].Count != 1 ||
		days.Rows[3].Count != 1 || days.Rows[4].Count != 0 || !days.Total.Total.Equal(d("300")) {
		t.Errorf("days: %+v", days.Rows)
	}

	months, err := f.svc.Sales(f.ctx, f.period(t, "2031-03-01", "2031-06-30"))
	if err != nil {
		t.Fatal(err)
	}
	if months.Granularity != model.ByMonth || len(months.Rows) != 4 || !months.Rows[0].Total.Equal(d("300")) ||
		months.Rows[1].Count != 0 || !months.Rows[2].Total.Equal(d("400")) || months.Rows[2].Start.Month() != time.May {
		t.Errorf("months: %+v", months.Rows)
	}

	empty, err := f.svc.Sales(f.ctx, f.period(t, "2031-03-03", ""))
	if err != nil || len(empty.Rows) != 0 || empty.Total.Count != 0 || len(empty.Methods) != 0 || !empty.Total.AvgTicket().IsZero() {
		t.Errorf("a day without sales: %+v, %v", empty, err)
	}
}

func TestCash(t *testing.T) {
	f := newFixture(t)
	// Sesión 1: abre el 10 a las 8 con $1.000 de cambio; cierra justa en
	// efectivo pero falta $100 de transferencia.
	s1 := f.session(t, f.at(3, 10, 8, 0), "1000")
	f.sale(t, s1, f.at(3, 10, 9, 0), false, []item{{f.unit, "2", "605", "300", "21"}}, // 1000 sin IVA, costo 600
		map[string]string{"CASH": "610", "TRANSFER": "600"})
	f.sale(t, s1, f.at(3, 10, 10, 0), false, []item{{f.unit, "1", "500", "0", "21"}}, map[string]string{"CASH": "500"}) // sin costo
	f.sale(t, s1, f.at(3, 10, 11, 0), true, []item{{f.unit, "1", "900", "300", "21"}}, map[string]string{"CASH": "900"})
	f.close(t, s1, f.at(3, 10, 14, 0), map[string]string{"CASH": "2110", "TRANSFER": "500"})
	// Sesión 2: el 10 a la tarde, cierra el 11 a la 1 (es del 10); sobran $20.
	s2 := f.session(t, f.at(3, 10, 14, 30), "0")
	f.sale(t, s2, f.at(3, 10, 20, 0), false, []item{{f.weight, "0.5", "2420", "1000", "21"}}, // 1210: 1000 sin IVA, costo 500
		map[string]string{"QR": "1210"})
	f.close(t, s2, f.at(3, 11, 1, 0), map[string]string{"QR": "1230"})
	// Sesión 3: abierta, no sale.
	s3 := f.session(t, f.at(3, 10, 23, 0), "0")
	f.sale(t, s3, f.at(3, 10, 23, 30), false, []item{{f.unit, "1", "100", "60", "21"}}, map[string]string{"CASH": "100"})

	r, err := f.svc.Cash(f.ctx, f.period(t, "2031-03-10", ""))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sessions) != 2 || r.Sessions[0].Session.ID != s1 || r.Sessions[1].Session.ID != s2 || r.Open != 1 {
		t.Fatalf("sessions: %d, open %d", len(r.Sessions), r.Open)
	}
	one := r.Sessions[0]
	if one.Sales != 2 || !one.Total.Equal(d("1710")) || one.Voided != 1 || !one.Difference().Equal(d("-100")) ||
		!one.Close.Line("CASH").Expected.Equal(d("2110")) {
		t.Errorf("session 1: %+v, difference %v", one.SessionTally, one.Difference())
	}
	if !one.Margin.Amount().Equal(d("400")) || !one.Margin.Percent().Equal(d("40")) || !one.Margin.NoCost.Equal(d("500")) {
		t.Errorf("session 1 margin: %+v", one.Margin)
	}
	if !r.Sessions[1].Difference().Equal(d("20")) {
		t.Errorf("session 2 difference: %v", r.Sessions[1].Difference())
	}
	if r.Sales != 3 || !r.Total.Equal(d("2920")) || r.Voided != 1 || !r.VoidedTotal.Equal(d("900")) ||
		!r.Difference.Equal(d("-80")) || !r.Margin.Amount().Equal(d("900")) || !r.Margin.Percent().Equal(d("45")) ||
		!r.Margin.NoCost.Equal(d("500")) {
		t.Errorf("totals: %+v, margin %v", r, r.Margin.Amount())
	}

	none, err := f.svc.Cash(f.ctx, f.period(t, "2031-03-11", ""))
	if err != nil || len(none.Sessions) != 0 || none.Open != 0 || !none.Margin.Amount().IsZero() {
		t.Errorf("the 11th: %+v, %v", none, err)
	}
}
