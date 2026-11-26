package sessions

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/usecasetest"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// openWithSales abre "Caja 1" con $ 5.000 de cambio y guarda tres ventas:
// $ 5.000 (2.000 efectivo + 3.000 débito), $ 1.800 en efectivo (después
// anulada) y $ 700 por QR.
// scan corre una consulta de una fila y guarda su resultado en dst.
func (f *fixture) scan(t *testing.T, dst any, q string, args ...any) {
	t.Helper()
	if err := f.tx.QueryRowContext(f.ctx, q, args...).Scan(dst); err != nil {
		t.Fatalf("fixture %q: %v", q, err)
	}
}

func (f *fixture) openWithSales(t *testing.T) *model.CashierSession {
	t.Helper()
	if _, err := f.svc.CreateRegister(f.ctx); err != nil {
		t.Fatal(err)
	}
	sess, err := f.svc.Open(f.ctx, f.userID, dec("5000"))
	if err != nil {
		t.Fatal(err)
	}
	var taxID, categoryID, productID int64
	f.scan(t, &taxID, `INSERT INTO taxes (name, rate) VALUES ('IVA test cierre', 21) RETURNING id;`)
	f.scan(t, &categoryID, `INSERT INTO categories (name, tax_id) VALUES ('Categoria test cierre', $1) RETURNING id;`, taxID)
	f.scan(t, &productID, `INSERT INTO products (name, category_id, sale_type, measure_unit)
		VALUES ('SODA TEST CIERRE', $1, 'UNIT', 'UNIT') RETURNING id;`, categoryID)

	sales := postgres.NewSaleStore(f.tx)
	add := func(total string, payments ...*model.Payment) *model.Sale {
		n, err := sales.NextNumber(f.ctx, sess.CashRegisterID)
		if err != nil {
			t.Fatal(err)
		}
		sale := &model.Sale{CashierSessionID: sess.ID, SaleNumber: n, Subtotal: dec(total), TaxAmount: dec("0"),
			Total: dec(total), Payments: payments,
			Items: []*model.SaleItem{{ProductID: productID, Quantity: dec("1"), UnitPrice: dec(total), UnitCost: dec("1"), TaxRate: dec("21")}}}
		if err := sales.Create(f.ctx, sale); err != nil {
			t.Fatalf("fixture sale: %v", err)
		}
		return sale
	}
	add("5000", &model.Payment{Method: model.PaymentCash, Amount: dec("2000")},
		&model.Payment{Method: model.PaymentDebit, Amount: dec("3000")})
	voided := add("1800", &model.Payment{Method: model.PaymentCash, Amount: dec("1800")})
	add("700", &model.Payment{Method: model.PaymentQR, Amount: dec("700")})
	if _, err := sales.Void(f.ctx, voided.ID, "test"); err != nil {
		t.Fatal(err)
	}
	return sess
}

func TestClose(t *testing.T) {
	f := newFixture(t)
	sess := f.openWithSales(t)

	// Lo que se muestra al cerrar: cuántos cobros por medio (la anulada no
	// cuenta), sin montos.
	counts, err := f.svc.PaymentCounts(f.ctx, sess.ID)
	if err != nil || len(counts) != 3 || counts[model.PaymentCash] != 1 || counts[model.PaymentDebit] != 1 ||
		counts[model.PaymentQR] != 1 {
		t.Errorf("payment counts: got %v, %v", counts, err)
	}

	// Cierre ciego: abierta, no hay resumen.
	if _, err := f.svc.Summary(f.ctx, sess.ID); !errors.Is(err, ErrSessionStillOpen) || !errors.Is(err, service.ErrConflict) {
		t.Errorf("summary of an open session: got %v", err)
	}

	// Faltan 50 en efectivo; QR sin declarar (faltan 700).
	sum, err := f.svc.Close(f.ctx, f.userID, sess.ID, []*model.CashAudit{
		{Method: model.PaymentCash, DeclaredAmount: dec("6950")},
		{Method: model.PaymentDebit, DeclaredAmount: dec("3000")},
	})
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	usecasetest.WantAuthor(t, f.ctx, f.tx, "closed by", usecasetest.Author("cashier_sessions", "closed_by", "id = $2"), f.userID, sess.ID)
	usecasetest.WantAuthor(t, f.ctx, f.tx, "close audit", usecasetest.Author("audit_log", "user_id",
		"table_name = 'cashier_sessions' AND action = 'CLOSE' AND record_id = $2"), f.userID, sess.ID)
	if sum.Session.Status != model.SessionClosed || sum.Session.ClosingAmount == nil ||
		!sum.Session.ClosingAmount.Equal(dec("6950")) || sum.Session.ClosedAt == nil {
		t.Errorf("closed session: got %+v", sum.Session)
	}
	if sum.Sales != 2 || sum.Voided != 1 || !sum.Total.Equal(dec("5700")) {
		t.Errorf("counts: got %d sales, %d voided, total %s", sum.Sales, sum.Voided, sum.Total)
	}
	want := map[string][3]string{ // esperado, declarado, diferencia
		model.PaymentCash:  {"7000", "6950", "-50"}, // 5.000 de cambio + 2.000 (la anulada no cuenta)
		model.PaymentDebit: {"3000", "3000", "0"},
		model.PaymentQR:    {"700", "0", "-700"},
	}
	for m, w := range want {
		l := sum.Close.Line(m)
		if !l.Expected.Equal(dec(w[0])) || !l.Declared.Equal(dec(w[1])) || !l.Difference().Equal(dec(w[2])) {
			t.Errorf("%s: got %s / %s / %s, want %v", m, l.Expected, l.Declared, l.Difference(), w)
		}
	}

	// Se guardó una declaración por medio (las 5).
	audits, err := postgres.NewCashAuditStore(f.tx).ListBySession(f.ctx, sess.ID)
	if err != nil || len(audits) != len(model.PaymentMethods) {
		t.Errorf("cash audits: got %d, %v", len(audits), err)
	}

	// Queda auditado, con las diferencias.
	var raw []byte
	if err := f.tx.QueryRowContext(f.ctx, `
		SELECT new_data FROM audit_log
		WHERE table_name = 'cashier_sessions' AND action = 'CLOSE' AND record_id = $1;`, sess.ID).Scan(&raw); err != nil {
		t.Fatalf("audit log: %v", err)
	}
	var logged struct {
		Sales         int  `json:"sales"`
		HasDifference bool `json:"has_difference"`
		Lines         []struct {
			Method     string          `json:"method"`
			Difference decimal.Decimal `json:"difference"`
		} `json:"lines"`
	}
	if err := json.Unmarshal(raw, &logged); err != nil {
		t.Fatal(err)
	}
	if logged.Sales != 2 || !logged.HasDifference || len(logged.Lines) != 5 ||
		logged.Lines[0].Method != model.PaymentCash || !logged.Lines[0].Difference.Equal(dec("-50")) {
		t.Errorf("audit data: got %s", raw)
	}

	// Cerrada: el resumen es el mismo, leído de la base.
	again, err := f.svc.Summary(f.ctx, sess.ID)
	if err != nil || again.Sales != 2 || again.Voided != 1 || !again.Total.Equal(dec("5700")) {
		t.Fatalf("summary: got %+v, %v", again, err)
	}
	for m, w := range want {
		if l := again.Close.Line(m); !l.Difference().Equal(dec(w[2])) {
			t.Errorf("summary %s: got %s, want %s", m, l.Difference(), w[2])
		}
	}

	// Ya cerrada no se cierra de nuevo, y la caja vuelve a abrir.
	if _, err := f.svc.Close(f.ctx, f.userID, sess.ID, nil); !errors.Is(err, store.ErrSessionNotOpen) ||
		!errors.Is(err, service.ErrConflict) {
		t.Errorf("close twice: got %v", err)
	}
	if st, err := f.svc.Current(f.ctx); err != nil || st.Session != nil || st.LastClosed == nil ||
		st.LastClosed.ID != sess.ID {
		t.Errorf("after close: got %+v, %v (want no session and the last close)", st, err)
	}
	if _, err := f.svc.Open(f.ctx, f.userID, dec("0")); err != nil {
		t.Errorf("open after close: %v", err)
	}
}

func TestCloseValidation(t *testing.T) {
	f := newFixture(t)
	sess := f.openWithSales(t)

	_, err := f.svc.Close(f.ctx, f.userID, sess.ID, []*model.CashAudit{{Method: model.PaymentCash, DeclaredAmount: dec("-1")}})
	if !errors.Is(err, service.ErrValidation) || !errors.Is(err, model.ErrInvalidDeclaredAmount) {
		t.Errorf("negative: got %v", err)
	}
	_, err = f.svc.Close(f.ctx, f.userID, sess.ID, []*model.CashAudit{
		{Method: model.PaymentCash, DeclaredAmount: dec("1")},
		{Method: model.PaymentCash, DeclaredAmount: dec("2")},
	})
	if !errors.Is(err, model.ErrCashAuditDuplicateMethod) {
		t.Errorf("twice: got %v", err)
	}
	// Nada quedó a medias: sigue abierta y sin declaraciones.
	if st, err := f.svc.Current(f.ctx); err != nil || st.Session == nil || st.Session.ID != sess.ID {
		t.Errorf("still open: got %+v, %v", st, err)
	}
	if audits, _ := postgres.NewCashAuditStore(f.tx).ListBySession(f.ctx, sess.ID); len(audits) != 0 {
		t.Errorf("no audits after a failed close: got %d", len(audits))
	}

	if _, err := f.svc.Close(f.ctx, f.userID, -1, nil); !errors.Is(err, store.ErrSessionNotOpen) {
		t.Errorf("unknown session: got %v", err)
	}
	if _, err := f.svc.Summary(f.ctx, 999999999); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("summary unknown: got %v", err)
	}
	if _, err := f.svc.Summary(f.ctx, 0); !errors.Is(err, service.ErrInvalidID) {
		t.Errorf("summary bad id: got %v", err)
	}
}

func TestCloseWithoutSales(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.CreateRegister(f.ctx); err != nil {
		t.Fatal(err)
	}
	sess, err := f.svc.Open(f.ctx, f.userID, dec("0"))
	if err != nil {
		t.Fatal(err)
	}
	sum, err := f.svc.Close(f.ctx, f.userID, sess.ID, nil)
	if err != nil || sum.Sales != 0 || sum.Voided != 0 || !sum.Total.IsZero() || sum.Close.HasDifference() {
		t.Errorf("empty close: got %+v, %v", sum, err)
	}
}
