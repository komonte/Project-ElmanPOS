package postgres_test

import (
	"errors"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Stores del cierre de caja (F-12) y la anulación (F-08).

// saleIn guarda una venta de un renglón en la sesión, con los pagos dados
// (los totales tienen que cerrar: acá no se recalculan).
func (f *productFixture) saleIn(t *testing.T, s *model.CashierSession, productID int64, total string,
	payments ...*model.Payment) *model.Sale {
	t.Helper()
	sales := postgres.NewSaleStore(f.tx)
	n, err := sales.NextNumber(f.ctx, s.CashRegisterID)
	if err != nil {
		t.Fatal(err)
	}
	sale := &model.Sale{
		CashierSessionID: s.ID, SaleNumber: n, Subtotal: d(total), TaxAmount: d("0"), Total: d(total),
		Items:    []*model.SaleItem{{ProductID: productID, Quantity: d("1"), UnitPrice: d(total), UnitCost: d("1"), TaxRate: d("0")}},
		Payments: payments,
	}
	if err := sales.Create(f.ctx, sale); err != nil {
		t.Fatalf("fixture sale: %v", err)
	}
	return sale
}

func TestCashierSessionStore_Close(t *testing.T) {
	f := newProductFixture(t)
	sessions := postgres.NewCashierSessionStore(f.tx)
	s := f.openSession(t)

	closed, err := sessions.Close(f.ctx, s.ID, d("17400.50"))
	if err != nil {
		t.Fatalf("close: %v", err)
	}
	if closed.Status != model.SessionClosed || closed.ClosingAmount == nil ||
		!closed.ClosingAmount.Equal(d("17400.50")) || closed.ClosedAt == nil || closed.RegisterName != "Caja test" {
		t.Errorf("close: got %+v", closed)
	}
	if _, err := sessions.Close(f.ctx, s.ID, d("1")); !errors.Is(err, store.ErrSessionNotOpen) {
		t.Errorf("close twice: got %v, want %v", err, store.ErrSessionNotOpen)
	}
	if _, err := sessions.Close(f.ctx, -1, d("1")); !errors.Is(err, store.ErrSessionNotOpen) {
		t.Errorf("close unknown: got %v, want %v", err, store.ErrSessionNotOpen)
	}

	// Get lee también una cerrada (el resumen del cierre).
	got, err := sessions.Get(f.ctx, s.ID)
	if err != nil || got.Status != model.SessionClosed || !got.ClosingAmount.Equal(d("17400.50")) {
		t.Errorf("get closed: got %+v, %v", got, err)
	}
	if _, err := sessions.Get(f.ctx, -1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get unknown: got %v, want %v", err, store.ErrNotFound)
	}
}

func TestCashAuditStore(t *testing.T) {
	f := newProductFixture(t)
	audits := postgres.NewCashAuditStore(f.tx)
	s := f.openSession(t)

	if list, err := audits.ListBySession(f.ctx, s.ID); err != nil || list == nil || len(list) != 0 {
		t.Errorf("none yet: got %v, %v (want empty, not nil)", list, err)
	}
	cash := &model.CashAudit{CashierSessionID: s.ID, Method: model.PaymentCash, DeclaredAmount: d("17400")}
	qr := &model.CashAudit{CashierSessionID: s.ID, Method: model.PaymentQR, DeclaredAmount: d("0")}
	for _, a := range []*model.CashAudit{cash, qr} {
		if err := audits.Create(f.ctx, a); err != nil || a.ID == 0 || a.CreatedAt == "" {
			t.Fatalf("create %s: %+v, %v", a.Method, a, err)
		}
	}
	list, err := audits.ListBySession(f.ctx, s.ID)
	if err != nil || len(list) != 2 || list[0].Method != model.PaymentCash || !list[0].DeclaredAmount.Equal(d("17400")) ||
		list[1].Method != model.PaymentQR {
		t.Errorf("list: got %+v, %v", list, err)
	}

	err = f.failing(t, func() error {
		return audits.Create(f.ctx, &model.CashAudit{CashierSessionID: s.ID, Method: model.PaymentCash, DeclaredAmount: d("1")})
	})
	if !errors.Is(err, store.ErrDuplicate) {
		t.Errorf("cash twice: got %v, want %v", err, store.ErrDuplicate)
	}
	err = f.failing(t, func() error {
		return audits.Create(f.ctx, &model.CashAudit{CashierSessionID: -1, Method: model.PaymentCash})
	})
	if !errors.Is(err, store.ErrInvalidReference) {
		t.Errorf("unknown session: got %v, want %v", err, store.ErrInvalidReference)
	}
}

func TestSaleStore_SessionAndVoid(t *testing.T) {
	f := newProductFixture(t)
	sales := postgres.NewSaleStore(f.tx)
	s := f.openSession(t)
	soda := f.create(t, "TEST-CLOSE-1", "SODA CIERRE TEST")

	if paid, err := sales.PaidByMethod(f.ctx, s.ID); err != nil || len(paid) != 0 {
		t.Errorf("no sales: got %v, %v", paid, err)
	}
	first := f.saleIn(t, s, soda.ID, "5000",
		&model.Payment{Method: model.PaymentCash, Amount: d("2000")},
		&model.Payment{Method: model.PaymentDebit, Amount: d("3000"), Reference: "1234"})
	second := f.saleIn(t, s, soda.ID, "1800", &model.Payment{Method: model.PaymentCash, Amount: d("1800")})
	third := f.saleIn(t, s, soda.ID, "700", &model.Payment{Method: model.PaymentQR, Amount: d("700")})

	paid, err := sales.PaidByMethod(f.ctx, s.ID)
	if err != nil || len(paid) != 3 || !paid[model.PaymentCash].Equal(d("3800")) ||
		!paid[model.PaymentDebit].Equal(d("3000")) || !paid[model.PaymentQR].Equal(d("700")) {
		t.Errorf("paid: got %v, %v", paid, err)
	}

	// Anular la segunda: se traba, cambia el estado y deja de contar.
	locked, err := sales.Lock(f.ctx, second.ID)
	if err != nil || locked.ID != second.ID || locked.Status != model.SaleCompleted ||
		locked.CashierSessionID != s.ID || !locked.Total.Equal(d("1800")) {
		t.Fatalf("lock: got %+v, %v", locked, err)
	}
	voidedAt, err := sales.Void(f.ctx, second.ID, "cobré de más")
	if err != nil || voidedAt == "" {
		t.Fatalf("void: got %q, %v", voidedAt, err)
	}
	if _, err := sales.Void(f.ctx, second.ID, "otra vez"); !errors.Is(err, store.ErrSaleNotCompleted) {
		t.Errorf("void twice: got %v, want %v", err, store.ErrSaleNotCompleted)
	}
	if _, err := sales.Void(f.ctx, -1, "x"); !errors.Is(err, store.ErrSaleNotCompleted) {
		t.Errorf("void unknown: got %v, want %v", err, store.ErrSaleNotCompleted)
	}
	// Un motivo en blanco lo rechaza la DB (chk_sales_void): el caso de uso
	// lo valida antes.
	if err := f.failing(t, func() error { _, err := sales.Void(f.ctx, first.ID, "  "); return err }); err == nil {
		t.Error("blank reason must fail")
	}
	if _, err := sales.Lock(f.ctx, -1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("lock unknown: got %v, want %v", err, store.ErrNotFound)
	}

	paid, err = sales.PaidByMethod(f.ctx, s.ID)
	if err != nil || !paid[model.PaymentCash].Equal(d("2000")) {
		t.Errorf("voided sale must not count: got %v, %v", paid, err)
	}

	got, err := sales.Get(f.ctx, second.ID)
	if err != nil || got.Status != model.SaleVoided || got.VoidedAt == nil || got.VoidReason != "cobré de más" {
		t.Errorf("get voided: got %+v, %v", got, err)
	}
	if got, err := sales.Get(f.ctx, first.ID); err != nil || got.VoidedAt != nil || got.VoidReason != "" {
		t.Errorf("get completed: got %+v, %v", got, err)
	}

	// La lista: la última primero, con sus pagos y la anulación.
	list, err := sales.ListBySession(f.ctx, s.ID)
	if err != nil || len(list) != 3 {
		t.Fatalf("list: got %d, %v", len(list), err)
	}
	if list[0].ID != third.ID || list[1].ID != second.ID || list[2].ID != first.ID {
		t.Errorf("list order: got %d, %d, %d", list[0].ID, list[1].ID, list[2].ID)
	}
	if l := list[1]; l.Status != model.SaleVoided || l.VoidReason != "cobré de más" || l.VoidedAt == nil {
		t.Errorf("list voided: got %+v", l)
	}
	if l := list[2]; len(l.Payments) != 2 || l.Payments[1].Method != model.PaymentDebit || l.Payments[1].Reference != "1234" {
		t.Errorf("list payments: got %+v", l.Payments)
	}
	if empty, err := sales.ListBySession(f.ctx, f.openSession(t).ID); err != nil || empty == nil || len(empty) != 0 {
		t.Errorf("no sales: got %v, %v (want empty, not nil)", empty, err)
	}
}

func TestBatchStore_RestoreAndSaleMovements(t *testing.T) {
	f := newProductFixture(t)
	s := f.openSession(t)
	storeID := f.storeOf(t, s)
	mortadela := f.create(t, "TEST-CLOSE-2", "MORTADELA CIERRE TEST")
	batches := postgres.NewBatchStore(f.tx)
	batchID := f.insertID(t, `
		INSERT INTO product_batches (product_id, batch_number, original_quantity, remaining_quantity, unit_cost)
		VALUES ($1, 'L1', 5, 5, 900) RETURNING id;`, mortadela.ID)

	// Se vende todo el lote: queda consumido.
	if _, left, err := batches.TakeFIFO(f.ctx, mortadela.ID, d("5")); err != nil || !left.IsZero() {
		t.Fatalf("take: left %s, %v", left, err)
	}
	b, err := batches.Restore(f.ctx, batchID, d("1.250"))
	if err != nil || !b.RemainingQuantity.Equal(d("1.250")) || !b.OriginalQuantity.Equal(d("5")) || b.IsConsumed {
		t.Errorf("restore: got %+v, %v", b, err)
	}
	if _, err := batches.Restore(f.ctx, -1, d("1")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("restore unknown: got %v, want %v", err, store.ErrNotFound)
	}
	// Más de lo que entró lo rechaza la DB.
	if err := f.failing(t, func() error { _, err := batches.Restore(f.ctx, batchID, d("4")); return err }); err == nil {
		t.Error("restoring above the original quantity must fail")
	}

	// Los movimientos SALE de una venta, en orden; el RETURN no entra.
	sale := f.saleIn(t, s, mortadela.ID, "2500", &model.Payment{Method: model.PaymentCash, Amount: d("2500")})
	movements := postgres.NewStockMovementStore(f.tx)
	if list, err := movements.SaleMovements(f.ctx, sale.ID); err != nil || list == nil || len(list) != 0 {
		t.Errorf("none yet: got %v, %v (want empty, not nil)", list, err)
	}
	for _, m := range []*model.StockMovement{
		{BatchID: &batchID, MovementType: model.MovementSale, Quantity: d("-0.300"), UnitCost: d("900")},
		{MovementType: model.MovementSale, Quantity: d("-0.050"), UnitCost: d("850")},
		{BatchID: &batchID, MovementType: model.MovementReturn, Quantity: d("0.300"), UnitCost: d("900")},
	} {
		m.ProductID, m.StoreID, m.SaleID = mortadela.ID, storeID, &sale.ID
		if _, err := movements.Create(f.ctx, m); err != nil {
			t.Fatalf("movement: %v", err)
		}
	}
	list, err := movements.SaleMovements(f.ctx, sale.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("sale movements: got %d, %v", len(list), err)
	}
	if list[0].BatchID == nil || *list[0].BatchID != batchID || !list[0].Quantity.Equal(d("-0.300")) {
		t.Errorf("first: got %+v", list[0])
	}
	if list[1].BatchID != nil || !list[1].Quantity.Equal(d("-0.050")) || !list[1].UnitCost.Equal(d("850")) {
		t.Errorf("without batch: got %+v", list[1])
	}
}
