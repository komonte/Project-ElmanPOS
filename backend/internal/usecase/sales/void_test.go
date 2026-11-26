package sales

import (
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
)

// sell cobra 2 sodas y 2,7 kg de queso (lote A entero, lote B entero y
// 0,2 kg sin lote) en efectivo justo.
func (f *fixture) sell(t *testing.T) *model.Sale {
	t.Helper()
	sale, err := f.svc.Checkout(f.ctx, &model.Sale{
		Items:    []*model.SaleItem{line(f.soda, "2"), line(f.queso, "2.700")},
		Payments: []*model.Payment{pay(model.PaymentCash, "15500")},
	})
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	return sale
}

// batch devuelve lo que queda en un lote y si está consumido.
func (f *fixture) batch(t *testing.T, id int64) (decimal.Decimal, bool) {
	t.Helper()
	var remaining decimal.Decimal
	var consumed bool
	if err := f.tx.QueryRowContext(f.ctx,
		`SELECT remaining_quantity, is_consumed FROM product_batches WHERE id = $1;`, id).Scan(&remaining, &consumed); err != nil {
		t.Fatal(err)
	}
	return remaining, consumed
}

func TestVoid(t *testing.T) {
	f := newFixture(t)
	sale := f.sell(t)
	if rem, consumed := f.batch(t, f.batchA); !rem.IsZero() || !consumed {
		t.Fatalf("after the sale batch A: got %s, %v", rem, consumed)
	}

	voided, err := f.svc.Void(f.ctx, sale.ID, "  cobré de más  ")
	if err != nil {
		t.Fatalf("void: %v", err)
	}
	if voided.Status != model.SaleVoided || voided.VoidReason != "cobré de más" || voided.VoidedAt == nil ||
		len(voided.Items) != 2 || len(voided.Payments) != 1 {
		t.Errorf("voided: got %+v", voided)
	}

	// El stock volvió entero: al saldo y a cada lote, que deja de estar
	// consumido.
	if got := f.onHand(t, f.soda); !got.Equal(d("10")) {
		t.Errorf("soda on hand: got %s, want 10", got)
	}
	if got := f.onHand(t, f.queso); !got.Equal(d("3")) {
		t.Errorf("queso on hand: got %s, want 3", got)
	}
	for id, want := range map[int64]string{f.batchA: "1", f.batchB: "1.5"} {
		if rem, consumed := f.batch(t, id); !rem.Equal(d(want)) || consumed {
			t.Errorf("batch %d: got %s (consumed %v), want %s", id, rem, consumed, want)
		}
	}

	// Un RETURN por cada SALE, con el mismo lote, la cantidad al revés y el
	// mismo costo: por producto y lote, los movimientos de la venta suman 0.
	if sales, returns := f.count(t, `SELECT count(*) FROM stock_movements WHERE sale_id = $1 AND movement_type = 'SALE';`, sale.ID),
		f.count(t, `SELECT count(*) FROM stock_movements WHERE sale_id = $1 AND movement_type = 'RETURN';`, sale.ID); sales != 4 || returns != 4 {
		t.Errorf("movements: got %d SALE and %d RETURN, want 4 and 4", sales, returns)
	}
	if n := f.count(t, `
		SELECT count(*) FROM (
			SELECT product_id, batch_id, unit_cost FROM stock_movements WHERE sale_id = $1
			GROUP BY product_id, batch_id, unit_cost HAVING SUM(quantity) <> 0) x;`, sale.ID); n != 0 {
		t.Errorf("%d product/batch/cost groups do not net to 0", n)
	}
	if n := f.count(t, `SELECT count(*) FROM stock_movements WHERE sale_id = $1 AND movement_type = 'RETURN'
		AND performed_by IS NULL AND quantity > 0;`, sale.ID); n != 4 {
		t.Errorf("returns: got %d positive without author, want 4", n)
	}

	// Queda auditada.
	if n := f.count(t, `SELECT count(*) FROM audit_log WHERE table_name = 'sales' AND action = 'VOID'
		AND record_id = $1 AND new_data->>'reason' = 'cobré de más';`, sale.ID); n != 1 {
		t.Errorf("audit log: got %d rows", n)
	}

	// Ya anulada no se anula de nuevo (el stock no vuelve dos veces).
	if _, err := f.svc.Void(f.ctx, sale.ID, "otra vez"); !errors.Is(err, ErrSaleAlreadyVoided) ||
		!errors.Is(err, service.ErrConflict) {
		t.Errorf("void twice: got %v", err)
	}
	if got := f.onHand(t, f.soda); !got.Equal(d("10")) {
		t.Errorf("soda after a second void: got %s, want 10", got)
	}

	// Lo anulado se puede vender de nuevo.
	f.sell(t)
}

func TestVoidRejected(t *testing.T) {
	f := newFixture(t)
	sale := f.sell(t)

	tests := []struct {
		name   string
		id     int64
		reason string
		want   error
	}{
		{"no reason", sale.ID, "   ", model.ErrVoidReasonRequired},
		{"long reason", sale.ID, strings.Repeat("x", 301), model.ErrVoidReasonTooLong},
		{"bad id", 0, "x", service.ErrInvalidID},
		{"unknown sale", 999999999, "x", service.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := f.svc.Void(f.ctx, tt.id, tt.reason); !errors.Is(err, tt.want) {
				t.Errorf("got %v, want %v", err, tt.want)
			}
		})
	}
	// Nada cambió.
	if got := f.onHand(t, f.soda); !got.Equal(d("8")) {
		t.Errorf("soda: got %s, want 8", got)
	}
	if n := f.count(t, `SELECT count(*) FROM sales WHERE id = $1 AND status = 'COMPLETED';`, sale.ID); n != 1 {
		t.Error("the sale must stay completed")
	}

	// Una venta de una sesión ya cerrada no se anula: su plata salió del
	// arqueo de ese día.
	f.exec(t, `UPDATE cashier_sessions SET status = 'CLOSED', closed_at = now() WHERE id = $1;`, f.sessionID)
	if _, err := f.svc.Void(f.ctx, sale.ID, "x"); !errors.Is(err, ErrRegisterClosed) {
		t.Errorf("register closed: got %v, want %v", err, ErrRegisterClosed)
	}
	f.exec(t, `INSERT INTO cashier_sessions (cash_register_id, opening_amount) VALUES ($1, 0);`, f.registerID)
	if _, err := f.svc.Void(f.ctx, sale.ID, "x"); !errors.Is(err, ErrSaleOtherSession) || !errors.Is(err, service.ErrConflict) {
		t.Errorf("other session: got %v, want %v", err, ErrSaleOtherSession)
	}
	if got := f.onHand(t, f.soda); !got.Equal(d("8")) {
		t.Errorf("soda after rejected voids: got %s, want 8", got)
	}
}
