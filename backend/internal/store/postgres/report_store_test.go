package postgres_test

import (
	"testing"
	"time"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Reportes (R-06, R-07). Las ventas de prueba son de 2031: así no se
// mezclan con las que ya haya en la base.

// line es un renglón de una venta de prueba.
type line struct {
	product              int64
	qty, price, cost, tx string
}

// pay es un pago de una venta de prueba.
type pay struct{ method, amount string }

// rawSale guarda una venta tal cual, con su hora, renglones y pagos. Si
// voided, queda anulada.
func (f *productFixture) rawSale(t *testing.T, sessionID int64, at time.Time, total string, voided bool, lines []line, pays ...pay) {
	t.Helper()
	var n int
	if err := f.tx.QueryRowContext(f.ctx, `SELECT COALESCE(MAX(sale_number), 0) + 1 FROM sales WHERE cashier_session_id = $1;`,
		sessionID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	status, voidedAt, reason := "COMPLETED", (*time.Time)(nil), (*string)(nil)
	if voided {
		r := "test"
		status, voidedAt, reason = "VOIDED", &at, &r
	}
	id := f.insertID(t, `INSERT INTO sales (cashier_session_id, sale_number, status, subtotal, tax_amount, total,
		created_at, voided_at, void_reason) VALUES ($1, $2, $3, $4, 0, $4, $5, $6, $7) RETURNING id;`,
		sessionID, n, status, total, at, voidedAt, reason)
	for _, l := range lines {
		exec(t, f.tx, `INSERT INTO sale_items (sale_id, product_id, quantity, unit_price, unit_cost, tax_rate)
			VALUES ($1, $2, $3, $4, $5, $6);`, id, l.product, l.qty, l.price, l.cost, l.tx)
	}
	for _, p := range pays {
		exec(t, f.tx, `INSERT INTO payments (sale_id, method, amount) VALUES ($1, $2, $3);`, id, p.method, p.amount)
	}
}

func TestReportStore_Sales(t *testing.T) {
	f := newProductFixture(t)
	reports := postgres.NewReportStore(f.tx)
	ba, _ := time.LoadLocation("America/Argentina/Buenos_Aires")
	unit := f.create(t, "TEST-REP-U", "PRODUCTO UNIDAD TEST")
	weight := f.product("TEST-REP-W", "PRODUCTO PESO TEST")
	weight.SaleType, weight.MeasureUnit = model.SaleTypeWeight, model.MeasureUnitKilogram
	weight, err := f.store.Create(f.ctx, weight)
	if err != nil {
		t.Fatal(err)
	}
	sess := f.openSession(t)

	at := func(day, hour, min int) time.Time { return time.Date(2031, 3, day, hour, min, 0, 0, ba) }
	// 10/3: dos ventas a las 9 (una dividida) y una a las 18 con kilos.
	f.rawSale(t, sess.ID, at(10, 9, 5), "300", false, []line{{unit.ID, "3", "100", "60", "21"}},
		pay{"CASH", "300"})
	f.rawSale(t, sess.ID, at(10, 9, 50), "500", false, []line{{unit.ID, "5", "100", "60", "21"}},
		pay{"CASH", "200"}, pay{"TRANSFER", "300"})
	f.rawSale(t, sess.ID, at(10, 18, 0), "1250", false, []line{{weight.ID, "1.250", "1000", "0", "10.5"}},
		pay{"DEBIT_CARD", "1250"})
	// Anulada: no cuenta en ventas ni pagos, sí en anuladas.
	f.rawSale(t, sess.ID, at(10, 9, 30), "999", true, []line{{unit.ID, "1", "999", "1", "21"}}, pay{"CASH", "999"})
	// 11/3 a las 00:30 en el local (03:30 UTC): es del 11, no del 10.
	f.rawSale(t, sess.ID, at(11, 0, 30), "100", false, []line{{unit.ID, "1", "100", "60", "21"}}, pay{"QR", "100"})
	// Fuera del período.
	f.rawSale(t, sess.ID, at(12, 10, 0), "100", false, []line{{unit.ID, "1", "100", "60", "21"}}, pay{"CASH", "100"})

	t.Run("by hour", func(t *testing.T) {
		p, _ := model.ParsePeriod("2031-03-10", "", ba, time.Now())
		got, err := reports.SalesByBucket(f.ctx, p.Buckets(), p.End())
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Bucket != 9 || got[0].Count != 2 || !got[0].Total.Equal(d("800")) ||
			!got[0].Units.Equal(d("8")) || !got[0].Kg.IsZero() ||
			got[1].Bucket != 18 || got[1].Count != 1 || !got[1].Kg.Equal(d("1.25")) || !got[1].Units.IsZero() {
			t.Errorf("sales by hour: got %+v", got)
		}
		pays, err := reports.PaymentsByBucket(f.ctx, p.Buckets(), p.End())
		if err != nil {
			t.Fatal(err)
		}
		want := []model.PaymentTally{{Bucket: 9, Method: "CASH", Count: 2, Amount: d("500")},
			{Bucket: 9, Method: "TRANSFER", Count: 1, Amount: d("300")}, {Bucket: 18, Method: "DEBIT_CARD", Count: 1, Amount: d("1250")}}
		if len(pays) != len(want) {
			t.Fatalf("payments by hour: got %+v", pays)
		}
		for i := range want {
			if pays[i].Bucket != want[i].Bucket || pays[i].Method != want[i].Method || pays[i].Count != want[i].Count ||
				!pays[i].Amount.Equal(want[i].Amount) {
				t.Errorf("payment %d: got %+v, want %+v", i, pays[i], want[i])
			}
		}
		n, total, err := reports.Voided(f.ctx, p.Start(), p.End())
		if err != nil || n != 1 || !total.Equal(d("999")) {
			t.Errorf("voided: got %d, %v, %v", n, total, err)
		}
	})

	t.Run("by day: midnight in the store", func(t *testing.T) {
		p, _ := model.ParsePeriod("2031-03-10", "2031-03-11", ba, time.Now())
		got, err := reports.SalesByBucket(f.ctx, p.Buckets(), p.End())
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 2 || got[0].Bucket != 0 || got[0].Count != 3 || !got[0].Total.Equal(d("2050")) ||
			got[1].Bucket != 1 || got[1].Count != 1 || !got[1].Total.Equal(d("100")) {
			t.Errorf("sales by day: got %+v", got)
		}
	})

	t.Run("nothing", func(t *testing.T) {
		p, _ := model.ParsePeriod("2031-03-20", "", ba, time.Now())
		got, err := reports.SalesByBucket(f.ctx, p.Buckets(), p.End())
		if err != nil || len(got) != 0 {
			t.Errorf("empty: got %+v, %v", got, err)
		}
		if n, total, err := reports.Voided(f.ctx, p.Start(), p.End()); err != nil || n != 0 || !total.IsZero() {
			t.Errorf("no voided: got %d, %v, %v", n, total, err)
		}
	})
}

func TestReportStore_ClosedSessions(t *testing.T) {
	f := newProductFixture(t)
	reports := postgres.NewReportStore(f.tx)
	ba, _ := time.LoadLocation("America/Argentina/Buenos_Aires")
	unit := f.create(t, "TEST-REP-S", "PRODUCTO CAJA TEST")
	ana := f.user(t, "test.ana.rep", "Ana Rep")
	at := func(day, hour int) time.Time { return time.Date(2031, 3, day, hour, 0, 0, 0, ba) }

	// La sesión del 10/3 abre a las 20 y cierra a la 1 del 11: es del 10.
	closed := f.openSession(t)
	exec(t, f.tx, `UPDATE cashier_sessions SET opened_at = $2, opened_by = $3 WHERE id = $1;`, closed.ID, at(10, 20), ana)
	// IVA 21%: 1210 con IVA son 1000 sin IVA; costó 600 → margen 400.
	f.rawSale(t, closed.ID, at(10, 21), "1210", false, []line{{unit.ID, "2", "605", "300", "21"}},
		pay{"CASH", "1000"}, pay{"QR", "210"})
	// Sin costo: va aparte, con IVA.
	f.rawSale(t, closed.ID, at(10, 22), "500", false, []line{{unit.ID, "1", "500", "0", "21"}}, pay{"CASH", "500"})
	f.rawSale(t, closed.ID, at(10, 23), "700", true, []line{{unit.ID, "1", "700", "100", "21"}}, pay{"CASH", "700"})
	exec(t, f.tx, `UPDATE cashier_sessions SET status = 'CLOSED', closed_at = $2, closed_by = $3, closing_amount = 2400
		WHERE id = $1;`, closed.ID, at(11, 1), ana)
	for _, a := range []struct{ m, amount string }{{"CASH", "2400"}, {"DEBIT_CARD", "0"}, {"CREDIT_CARD", "0"}, {"TRANSFER", "0"}, {"QR", "210"}} {
		exec(t, f.tx, `INSERT INTO cash_audits (cashier_session_id, method, declared_amount) VALUES ($1, $2, $3);`,
			closed.ID, a.m, a.amount)
	}

	// Otra caja, abierta el 10: no sale en la lista, se cuenta como abierta.
	open := f.openSession(t)
	exec(t, f.tx, `UPDATE cashier_sessions SET opened_at = $2 WHERE id = $1;`, open.ID, at(10, 9))

	p, _ := model.ParsePeriod("2031-03-10", "", ba, time.Now())
	got, err := reports.ClosedSessions(f.ctx, p.Start(), p.End())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("closed sessions: got %d", len(got))
	}
	s := got[0]
	if s.Session.ID != closed.ID || s.Session.RegisterName != "Caja test" || s.Session.OpenedByName == nil ||
		*s.Session.OpenedByName != "Ana Rep" || s.Session.ClosedByName == nil || s.Session.ClosedAt == nil {
		t.Errorf("session: got %+v", s.Session)
	}
	if s.Sales != 2 || !s.Total.Equal(d("1710")) || s.Voided != 1 || !s.VoidedTotal.Equal(d("700")) {
		t.Errorf("sales: got %d %v, voided %d %v", s.Sales, s.Total, s.Voided, s.VoidedTotal)
	}
	if !s.Margin.Net.Equal(d("1000")) || !s.Margin.Cost.Equal(d("600")) || !s.Margin.NoCost.Equal(d("500")) {
		t.Errorf("margin: got %+v", s.Margin)
	}
	if len(s.Paid) != 2 || !s.Paid["CASH"].Equal(d("1500")) || !s.Paid["QR"].Equal(d("210")) {
		t.Errorf("paid: got %v", s.Paid)
	}
	if len(s.Declared) != 5 || s.Declared[0].Method != "CASH" || !s.Declared[0].DeclaredAmount.Equal(d("2400")) {
		t.Errorf("declared: got %+v", s.Declared)
	}

	if n, err := reports.OpenSessions(f.ctx, p.Start(), p.End()); err != nil || n != 1 {
		t.Errorf("open sessions: got %d, %v", n, err)
	}
	// El 11 no tiene ninguna (la del 10 cerró el 11, pero es del 10).
	p11, _ := model.ParsePeriod("2031-03-11", "", ba, time.Now())
	if got, err := reports.ClosedSessions(f.ctx, p11.Start(), p11.End()); err != nil || len(got) != 0 {
		t.Errorf("the 11th: got %d, %v", len(got), err)
	}
}
