package postgres_test

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// openSession crea un local con una caja y le abre una sesión.
// user crea un usuario en la transacción del test y devuelve su id (F-13:
// quién hizo cada cosa).
func (f *productFixture) user(t *testing.T, username, name string) int64 {
	t.Helper()
	return f.insertID(t, `INSERT INTO users (username, name, password_hash, role)
	                      VALUES ($1, $2, 'x', 'CASHIER') RETURNING id;`, username, name)
}

func (f *productFixture) openSession(t *testing.T) *model.CashierSession {
	t.Helper()
	storeID := f.insertID(t, `INSERT INTO stores (name, address) VALUES ('Local test venta', 'Calle Test 123') RETURNING id;`)
	r, err := postgres.NewCashRegisterStore(f.tx).Create(f.ctx, storeID, "Caja test")
	if err != nil {
		t.Fatalf("fixture register: %v", err)
	}
	s, err := postgres.NewCashierSessionStore(f.tx).Open(f.ctx,
		&model.CashierSession{CashRegisterID: r.ID, OpeningAmount: d("1000")})
	if err != nil {
		t.Fatalf("fixture session: %v", err)
	}
	return s
}

func TestCashRegisterStore_Only(t *testing.T) {
	f := newProductFixture(t)
	registers := postgres.NewCashRegisterStore(f.tx)
	storeID := f.insertID(t, `INSERT INTO stores (name) VALUES ('Local test cajas') RETURNING id;`)

	if _, err := registers.Only(f.ctx, storeID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("no registers: got %v, want %v", err, store.ErrNotFound)
	}
	r, err := registers.Create(f.ctx, storeID, "  Caja 1 ")
	if err != nil {
		t.Fatal(err)
	}
	got, err := registers.Only(f.ctx, storeID)
	if err != nil || got.ID != r.ID || got.Name != "Caja 1" || !got.IsActive {
		t.Errorf("only: got %+v, %v", got, err)
	}

	// Una caja inactiva no cuenta.
	other, _ := registers.Create(f.ctx, storeID, "Caja vieja")
	f.deactivate(t, "cash_registers", other.ID)
	if got, err := registers.Only(f.ctx, storeID); err != nil || got.ID != r.ID {
		t.Errorf("inactive register counted: got %+v, %v", got, err)
	}
	if _, err := registers.Create(f.ctx, storeID, "Caja 2"); err != nil {
		t.Fatal(err)
	}
	if _, err := registers.Only(f.ctx, storeID); !errors.Is(err, store.ErrSeveralActiveRegisters) {
		t.Errorf("two registers: got %v, want %v", err, store.ErrSeveralActiveRegisters)
	}

	err = f.failing(t, func() error { _, err := registers.Create(f.ctx, -1, "Caja"); return err })
	if !errors.Is(err, store.ErrInvalidReference) {
		t.Errorf("unknown store: got %v, want %v", err, store.ErrInvalidReference)
	}
}

func TestCashierSessionStore(t *testing.T) {
	f := newProductFixture(t)
	sessions := postgres.NewCashierSessionStore(f.tx)
	s := f.openSession(t)
	if s.ID == 0 || s.Status != model.SessionOpen || s.OpenedBy != nil || !s.OpeningAmount.Equal(d("1000")) ||
		s.RegisterName != "Caja test" || s.OpenedAt == "" || s.ClosedAt != nil || s.ClosingAmount != nil {
		t.Errorf("open: got %+v", s)
	}

	got, err := sessions.OpenFor(f.ctx, s.CashRegisterID)
	if err != nil || got.ID != s.ID {
		t.Errorf("open for: got %+v, %v", got, err)
	}
	if got, err := sessions.LockOpen(f.ctx, s.ID); err != nil || got.ID != s.ID {
		t.Errorf("lock open: got %+v, %v", got, err)
	}

	err = f.failing(t, func() error {
		_, err := sessions.Open(f.ctx, &model.CashierSession{CashRegisterID: s.CashRegisterID})
		return err
	})
	if !errors.Is(err, store.ErrSessionAlreadyOpen) {
		t.Errorf("second open: got %v, want %v", err, store.ErrSessionAlreadyOpen)
	}
	err = f.failing(t, func() error {
		_, err := sessions.Open(f.ctx, &model.CashierSession{CashRegisterID: -1})
		return err
	})
	if !errors.Is(err, store.ErrInvalidReference) {
		t.Errorf("unknown register: got %v, want %v", err, store.ErrInvalidReference)
	}

	// Cerrada (el cierre llega con F-12): ya no está abierta ni se traba, y
	// la caja puede abrir otra.
	if _, err := f.tx.ExecContext(f.ctx,
		`UPDATE cashier_sessions SET status = 'CLOSED', closed_at = now() WHERE id = $1;`, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := sessions.OpenFor(f.ctx, s.CashRegisterID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("open for after close: got %v, want %v", err, store.ErrNotFound)
	}
	if _, err := sessions.LockOpen(f.ctx, s.ID); !errors.Is(err, store.ErrSessionNotOpen) {
		t.Errorf("lock closed: got %v, want %v", err, store.ErrSessionNotOpen)
	}
	if _, err := sessions.LockOpen(f.ctx, -1); !errors.Is(err, store.ErrSessionNotOpen) {
		t.Errorf("lock unknown: got %v, want %v", err, store.ErrSessionNotOpen)
	}
	if _, err := sessions.Open(f.ctx, &model.CashierSession{CashRegisterID: s.CashRegisterID}); err != nil {
		t.Errorf("open after close: %v", err)
	}
}

func TestSaleStore_CreateAndGet(t *testing.T) {
	f := newProductFixture(t)
	sales := postgres.NewSaleStore(f.tx)
	s := f.openSession(t)
	soda := f.create(t, "TEST-SALE-1", "SODA VENTA TEST")
	mortadela := f.create(t, "TEST-SALE-2", "MORTADELA VENTA TEST")
	batchID := f.insertID(t, `
		INSERT INTO product_batches (product_id, batch_number, original_quantity, remaining_quantity, unit_cost)
		VALUES ($1, 'L1', 5, 5, 900) RETURNING id;`, mortadela.ID)

	n, err := sales.NextNumber(f.ctx, s.CashRegisterID)
	if err != nil || n != 1 {
		t.Fatalf("first number: got %d, %v", n, err)
	}
	sale := &model.Sale{
		CashierSessionID: s.ID, SaleNumber: n,
		Subtotal: d("2875"), TaxAmount: d("498.97"), Total: d("2875"),
		Items: []*model.SaleItem{
			{ProductID: soda.ID, Quantity: d("2"), UnitPrice: d("1000"), UnitCost: d("600"), TaxRate: d("21")},
			{ProductID: mortadela.ID, Quantity: d("0.350"), UnitPrice: d("2500"), UnitCost: d("900"), TaxRate: d("21"),
				Batches: []*model.SaleItemBatch{{BatchID: batchID, Quantity: d("0.350"), UnitCost: d("900")}}},
		},
		Payments: []*model.Payment{
			{Method: model.PaymentCash, Amount: d("1875")},
			{Method: model.PaymentTransfer, Amount: d("1000"), Reference: "op 77"},
		},
	}
	if err := sales.Create(f.ctx, sale); err != nil {
		t.Fatalf("create: %v", err)
	}
	if sale.ID == 0 || sale.CreatedAt == "" || sale.Status != model.SaleCompleted ||
		sale.Items[1].ID == 0 || sale.Items[1].SaleID != sale.ID || sale.Items[1].Batches[0].ID == 0 ||
		sale.Items[1].Batches[0].SaleItemID != sale.Items[1].ID || sale.Payments[1].ID == 0 {
		t.Errorf("create: ids not filled: %+v", sale)
	}
	var refNull bool
	if err := f.tx.QueryRowContext(f.ctx, `SELECT reference IS NULL FROM payments WHERE id = $1;`,
		sale.Payments[0].ID).Scan(&refNull); err != nil || !refNull {
		t.Errorf("empty reference must be NULL: %v, %v", refNull, err)
	}

	got, err := sales.Get(f.ctx, sale.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.SaleNumber != 1 || !got.Total.Equal(d("2875")) || !got.TaxAmount.Equal(d("498.97")) ||
		got.RegisterName != "Caja test" || got.StoreName != "Local test venta" ||
		got.StoreAddress != "Calle Test 123" || len(got.Items) != 2 || len(got.Payments) != 2 {
		t.Fatalf("get: got %+v", got)
	}
	if it := got.Items[1]; it.ProductName != "MORTADELA VENTA TEST" || !it.Quantity.Equal(d("0.350")) ||
		!it.UnitPrice.Equal(d("2500")) || it.SaleType != model.SaleTypeUnit {
		t.Errorf("get item: got %+v", it)
	}
	if p := got.Payments[1]; p.Method != model.PaymentTransfer || p.Reference != "op 77" || !p.Amount.Equal(d("1000")) {
		t.Errorf("get payment: got %+v", p)
	}

	// Un local sin dirección (NULL) sale con la dirección vacía.
	if _, err := f.tx.ExecContext(f.ctx, `
		UPDATE stores SET address = NULL
		WHERE id = (SELECT store_id FROM cash_registers WHERE id = $1);`, s.CashRegisterID); err != nil {
		t.Fatal(err)
	}
	if got, err := sales.Get(f.ctx, sale.ID); err != nil || got.StoreAddress != "" {
		t.Errorf("get without address: got %q, %v", got.StoreAddress, err)
	}
	if _, err := sales.Get(f.ctx, -1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("get unknown: got %v, want %v", err, store.ErrNotFound)
	}

	// El movimiento de la venta lleva su sale_id.
	mv, err := postgres.NewStockMovementStore(f.tx).Create(f.ctx, &model.StockMovement{
		ProductID: mortadela.ID, StoreID: f.storeOf(t, s), BatchID: &batchID, MovementType: model.MovementSale,
		Quantity: d("-0.350"), UnitCost: d("900"), SaleID: &sale.ID})
	if err != nil || mv.SaleID == nil || *mv.SaleID != sale.ID {
		t.Errorf("sale movement: got %+v, %v", mv, err)
	}

	// El número sigue en la sesión siguiente de la misma caja.
	if _, err := f.tx.ExecContext(f.ctx, `UPDATE cashier_sessions SET status = 'CLOSED' WHERE id = $1;`, s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.NewCashierSessionStore(f.tx).Open(f.ctx,
		&model.CashierSession{CashRegisterID: s.CashRegisterID}); err != nil {
		t.Fatal(err)
	}
	if n, err := sales.NextNumber(f.ctx, s.CashRegisterID); err != nil || n != 2 {
		t.Errorf("number after a new session: got %d, %v, want 2", n, err)
	}
	// Otra caja empieza en 1.
	if n, err := sales.NextNumber(f.ctx, f.openSession(t).CashRegisterID); err != nil || n != 1 {
		t.Errorf("other register: got %d, %v, want 1", n, err)
	}

	sale.SaleNumber, sale.Items[0].ProductID = 99, -1
	err = f.failing(t, func() error { return sales.Create(f.ctx, sale) })
	if !errors.Is(err, store.ErrInvalidReference) {
		t.Errorf("unknown product: got %v, want %v", err, store.ErrInvalidReference)
	}
}

// storeOf devuelve el local de la caja de una sesión.
func (f *productFixture) storeOf(t *testing.T, s *model.CashierSession) int64 {
	t.Helper()
	var id int64
	if err := f.tx.QueryRowContext(f.ctx, `SELECT store_id FROM cash_registers WHERE id = $1;`,
		s.CashRegisterID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
