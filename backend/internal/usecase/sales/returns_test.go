package sales

import (
	"errors"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Devoluciones (0028).
func TestReturn(t *testing.T) {
	f, suelto, atado := comboFixture(t)
	var maria int64
	f.scan(t, &maria, `INSERT INTO customers (name) VALUES ('MARIA TEST DEVOL') RETURNING id;`)
	// 3 sodas (3000) + 1 atado (2500, 20 sueltos) = 5500: 2000 efectivo, 3500 fiado.
	sale, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		CustomerID: &maria,
		Items:      []*model.SaleItem{line(f.soda, "3"), line(atado, "1")},
		Payments:   []*model.Payment{pay(model.PaymentCash, "2000"), pay(model.PaymentAccount, "3500")},
	})
	if err != nil {
		t.Fatal(err)
	}
	sodaLine, atadoLine := sale.Items[0].ID, sale.Items[1].ID

	// 2 sodas a la góndola, devuelto por transferencia (no como pagó).
	r, err := f.svc.Return(f.ctx, f.userID, sale.ID, "no le gustó",
		[]ReturnLine{{SaleItemID: sodaLine, Quantity: d("2"), Restock: true}},
		[]*model.Payment{pay(model.PaymentTransfer, "2000")})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Total.Equal(d("2000")) {
		t.Errorf("total %s, want 2000", r.Total)
	}
	if got := f.onHand(t, f.soda); !got.Equal(d("9")) {
		t.Errorf("soda on hand %s, want 9 (10 - 3 + 2)", got)
	}
	// El atado vuelve en sueltos; a cuenta corriente baja la deuda.
	if _, err := f.svc.Return(f.ctx, f.userID, sale.ID, "cambió de idea",
		[]ReturnLine{{SaleItemID: atadoLine, Quantity: d("1"), Restock: true}},
		[]*model.Payment{pay(model.PaymentAccount, "2500")}); err != nil {
		t.Fatal(err)
	}
	if got := f.onHand(t, suelto); !got.Equal(d("50")) {
		t.Errorf("suelto on hand %s, want 50", got)
	}
	debts, _ := postgres.NewCustomerStore(f.tx).Debts(f.ctx, []int64{maria})
	if !debts[maria].Equal(d("1000")) {
		t.Errorf("debt %s, want 1000", debts[maria])
	}
	// La última soda, rota: merma, no vuelve al stock.
	if _, err := f.svc.Return(f.ctx, f.userID, sale.ID, "rota",
		[]ReturnLine{{SaleItemID: sodaLine, Quantity: d("1"), Restock: false}},
		[]*model.Payment{pay(model.PaymentCash, "1000")}); err != nil {
		t.Fatal(err)
	}
	if got := f.onHand(t, f.soda); !got.Equal(d("9")) {
		t.Errorf("soda after shrinkage %s, want 9", got)
	}
	// Ya no queda nada.
	_, err = f.svc.Return(f.ctx, f.userID, sale.ID, "otra",
		[]ReturnLine{{SaleItemID: sodaLine, Quantity: d("1"), Restock: true}},
		[]*model.Payment{pay(model.PaymentCash, "1000")})
	if !errors.Is(err, ErrReturnTooMuch) {
		t.Errorf("too much: err %v", err)
	}
	// El arqueo: efectivo 2000 - 1000 = 1000; transferencia -2000.
	paid, err := postgres.NewSaleStore(f.tx).PaidByMethod(f.ctx, f.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !paid[model.PaymentCash].Equal(d("1000")) || !paid[model.PaymentTransfer].Equal(d("-2000")) {
		t.Errorf("paid by method %v", paid)
	}
	// No se anula una venta con devoluciones.
	if _, err := f.svc.Void(f.ctx, f.userID, sale.ID, "x"); !errors.Is(err, ErrSaleHasReturns) {
		t.Errorf("void: err %v", err)
	}
}

func TestReturnRejected(t *testing.T) {
	f := newFixture(t)
	sale, err := f.svc.Checkout(f.ctx, f.userID, &model.Sale{
		Items:    []*model.SaleItem{line(f.soda, "2")},
		Payments: []*model.Payment{pay(model.PaymentCash, "2000")},
	})
	if err != nil {
		t.Fatal(err)
	}
	ln := sale.Items[0].ID
	cash := func(a string) []*model.Payment { return []*model.Payment{pay(model.PaymentCash, a)} }
	tests := []struct {
		name   string
		reason string
		lines  []ReturnLine
		pays   []*model.Payment
		want   error
	}{
		{"no reason", " ", []ReturnLine{{ln, d("1"), true}}, cash("1000"), model.ErrReturnReasonRequired},
		{"no lines", "x", nil, cash("1000"), model.ErrReturnNoLines},
		{"fraction of a unit", "x", []ReturnLine{{ln, d("0.5"), true}}, cash("500"), model.ErrQuantityNotWhole},
		{"more than sold", "x", []ReturnLine{{ln, d("3"), true}}, cash("3000"), ErrReturnTooMuch},
		{"unknown line", "x", []ReturnLine{{999999999, d("1"), true}}, cash("1000"), ErrReturnLineUnknown},
		{"money does not add up", "x", []ReturnLine{{ln, d("1"), true}}, cash("900"), ErrReturnPaymentsTotal},
		{"on account without customer", "x", []ReturnLine{{ln, d("1"), true}},
			[]*model.Payment{pay(model.PaymentAccount, "1000")}, ErrReturnAccount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := f.svc.Return(f.ctx, f.userID, sale.ID, tt.reason, tt.lines, tt.pays)
			if !errors.Is(err, tt.want) || !errors.Is(err, service.ErrValidation) {
				t.Errorf("err %v, want %v", err, tt.want)
			}
		})
	}
	if _, err := f.svc.Void(f.ctx, f.userID, sale.ID, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Return(f.ctx, f.userID, sale.ID, "x", []ReturnLine{{ln, d("1"), true}}, cash("1000")); !errors.Is(err, ErrReturnSaleVoided) {
		t.Errorf("voided: err %v", err)
	}
	v, err := f.svc.FindForReturn(f.ctx, sale.SaleNumber)
	if err != nil || v.Sale.ID != sale.ID {
		t.Errorf("find: %v %+v", err, v)
	}
}
