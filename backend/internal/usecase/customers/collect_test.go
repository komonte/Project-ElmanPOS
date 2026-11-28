package customers

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sales"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// Cobrar la deuda (0027): hasta lo que debe, en la sesión abierta, y suma
// al esperado de su medio en el arqueo.
func TestCollect(t *testing.T) {
	svc, tx, admin := newSvc(t)
	ctx := context.Background()
	id := func(q string, args ...any) int64 {
		t.Helper()
		var v int64
		if err := tx.QueryRow(q, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return v
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`UPDATE stores SET is_active = FALSE;`)
	store := id(`INSERT INTO stores (name) VALUES ('Local test cobro') RETURNING id;`)
	reg := id(`INSERT INTO cash_registers (store_id, name) VALUES ($1, 'Caja test') RETURNING id;`, store)
	sess := id(`INSERT INTO cashier_sessions (cash_register_id, opening_amount) VALUES ($1, 0) RETURNING id;`, reg)
	maria := id(`INSERT INTO customers (name) VALUES ('MARIA TEST COBRO') RETURNING id;`)
	sold := func(amount string, status string) {
		n := id(`SELECT COALESCE(MAX(sale_number), 0) + 1 FROM sales WHERE cashier_session_id = $1;`, sess)
		voided := status == "VOIDED"
		s := id(`INSERT INTO sales (cashier_session_id, sale_number, status, subtotal, tax_amount, total, customer_id,
			voided_at, void_reason) VALUES ($1, $2, $3, $4, 0, $4, $5,
			CASE WHEN $6 THEN now() END, CASE WHEN $6 THEN 'test' END) RETURNING id;`, sess, n, status, amount, maria, voided)
		exec(`INSERT INTO payments (sale_id, method, amount) VALUES ($1, 'ACCOUNT', $2);`, s, amount)
	}
	sold("3000", "COMPLETED")
	sold("500", "COMPLETED")
	sold("9999", "VOIDED")

	// Con débito, más que la deuda no.
	if _, err := svc.Collect(ctx, admin, maria, model.PaymentDebit, d("4000"), "op 1"); !errors.Is(err, ErrOverDebt) ||
		!errors.Is(err, service.ErrValidation) {
		t.Errorf("over debt by card: err %v", err)
	}
	if _, err := svc.Collect(ctx, admin, maria, model.PaymentAccount, d("100"), ""); !errors.Is(err, service.ErrValidation) {
		t.Errorf("paying with ACCOUNT: err %v", err)
	}
	got, err := svc.Collect(ctx, admin, maria, model.PaymentDebit, d("1000"), "op 2")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Debt.Equal(d("2500")) || !got.Change.IsZero() || got.Payment.CashierSessionID != sess {
		t.Errorf("partial: %+v", got)
	}
	// En efectivo entrega 5000 por 2500: vuelto 2500, deuda 0.
	got, err = svc.Collect(ctx, admin, maria, model.PaymentCash, d("5000"), "ignored")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Debt.IsZero() || !got.Change.Equal(d("2500")) || !got.Payment.Amount.Equal(d("2500")) || got.Payment.Reference != "" {
		t.Errorf("cash: %+v %+v", got, got.Payment)
	}
	if _, err := svc.Collect(ctx, admin, maria, model.PaymentCash, d("1"), ""); !errors.Is(err, ErrNoDebt) {
		t.Errorf("no debt: err %v", err)
	}
	if _, err := svc.Collect(ctx, admin, 999999999, model.PaymentCash, d("1"), ""); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unknown customer: err %v", err)
	}

	// El arqueo: el efectivo cobrado suma al esperado, el débito también, y
	// lo fiado no.
	paid, err := postgres.NewSaleStore(tx).PaidByMethod(ctx, sess)
	if err != nil {
		t.Fatal(err)
	}
	if !paid[model.PaymentCash].Equal(d("2500")) || !paid[model.PaymentDebit].Equal(d("1000")) {
		t.Errorf("paid by method: %v", paid)
	}
	audit, err := model.NewCloseSummary(d("0"), paid, nil)
	if err != nil {
		t.Fatal(err)
	}
	if l := audit.Line(model.PaymentCash); !l.Expected.Equal(d("2500")) {
		t.Errorf("expected cash %s", l.Expected)
	}

	// La cuenta: 3000, 500, -1000, -2500; saldo final 0.
	_, entries, err := svc.Statement(ctx, maria)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 || !entries[3].Balance.IsZero() || !entries[1].Balance.Equal(d("3500")) {
		t.Errorf("statement: %+v", entries)
	}
	debtors, err := svc.Debtors(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range debtors {
		if x.CustomerID == maria {
			t.Error("a customer who paid everything is a debtor")
		}
	}

	// Caja cerrada: no se cobra.
	exec(`UPDATE cashier_sessions SET status = 'CLOSED', closed_at = now() WHERE id = $1;`, sess)
	sold("100", "COMPLETED")
	if _, err := svc.Collect(ctx, admin, maria, model.PaymentCash, d("100"), ""); !errors.Is(err, sales.ErrRegisterClosed) {
		t.Errorf("closed register: err %v", err)
	}
}
