package sessions

import (
	"errors"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
)

func TestSales(t *testing.T) {
	f := newFixture(t)
	sess := f.openWithSales(t)

	list, err := f.svc.Sales(f.ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	// La última primero: 3 (QR), 2 (anulada), 1 (efectivo + débito).
	if len(list) != 3 || list[0].SaleNumber != 3 || list[1].SaleNumber != 2 || list[2].SaleNumber != 1 {
		t.Fatalf("order: got %d sales", len(list))
	}
	if v := list[1]; v.Status != model.SaleVoided || v.VoidReason != "test" || v.VoidedAt == nil {
		t.Errorf("voided sale: got %+v", v)
	}
	if first := list[2]; len(first.Payments) != 2 || first.Payments[0].Method != model.PaymentCash ||
		!first.Payments[0].Amount.Equal(dec("2000")) || first.Payments[1].Method != model.PaymentDebit {
		t.Errorf("payments of sale 1: got %+v", first.Payments)
	}

	// Una sesión sin ventas: lista vacía, no nil.
	empty, err := f.svc.Sales(f.ctx, sess.ID+1000000)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("no sales: got %v, %v", empty, err)
	}

	if _, err := f.svc.Sales(f.ctx, 0); !errors.Is(err, service.ErrInvalidID) {
		t.Errorf("id 0: got %v", err)
	}
}
