package sales

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Devoluciones (2026-11-28, migración 0028). Decisiones con el local:
//   - Siempre de una venta, de cualquier día (se busca por número): solo
//     lo vendido, a su precio, y no más de lo vendido menos lo ya devuelto.
//     El importe de cada renglón es su parte del total cobrado (si hubo
//     redondeo en efectivo, se prorratea) y la suma nunca pasa lo que queda
//     por devolver de la venta.
//   - Por renglón, vuelve a la góndola (un RETURN al costo del renglón; un
//     combo devuelve sus componentes; un artículo sin registrar no tiene
//     stock) o es merma (no mueve stock: lo vendido ya salió).
//   - La plata vuelve por cualquier medio, dividida si hace falta, y suma
//     justo el total. Sale de la sesión abierta de la caja. ACCOUNT baja la
//     deuda del cliente de la venta, hasta lo que debe.
//   - Solo el admin (permiso returns, en la ruta). Una venta anulada no se
//     devuelve, y una con devoluciones ya no se anula (Void).

var (
	ErrReturnSaleVoided    = fmt.Errorf("a voided sale cannot be returned: %w", service.ErrConflict)
	ErrReturnLineUnknown   = fmt.Errorf("that line is not of this sale: %w", service.ErrValidation)
	ErrReturnTooMuch       = fmt.Errorf("more than what is left to return: %w", service.ErrValidation)
	ErrReturnPaymentsTotal = fmt.Errorf("the money given back must add up to the return total: %w", service.ErrValidation)
	ErrReturnAccount       = fmt.Errorf("on account only for a sale on account, up to the customer's debt: %w", service.ErrValidation)
	ErrSaleHasReturns      = fmt.Errorf("a sale with returns cannot be voided: %w", service.ErrConflict)
)

// ReturnLine es lo que se pide devolver de un renglón.
type ReturnLine struct {
	SaleItemID int64
	Quantity   decimal.Decimal
	Restock    bool
}

// ReturnView es una venta para devolver: la venta, cuánto se devolvió ya de
// cada renglón, la plata ya devuelta y la que queda.
type ReturnView struct {
	Sale     *model.Sale
	Returned map[int64]decimal.Decimal
	Refunded decimal.Decimal
	Left     decimal.Decimal
}

// FindForReturn busca la venta número number para devolver.
// service.ErrNotFound si no existe.
func (s *Service) FindForReturn(ctx context.Context, number int) (*ReturnView, error) {
	id, err := postgres.NewSaleReturnStore(s.db).FindSaleByNumber(ctx, number)
	if err != nil {
		return nil, translate(err)
	}
	return s.returnView(ctx, s.db, id)
}

func (s *Service) returnView(ctx context.Context, db postgres.DBTX, saleID int64) (*ReturnView, error) {
	sale, err := postgres.NewSaleStore(db).Get(ctx, saleID)
	if err != nil {
		return nil, translate(err)
	}
	returned, refunded, err := postgres.NewSaleReturnStore(db).Returned(ctx, saleID)
	if err != nil {
		return nil, err
	}
	return &ReturnView{Sale: sale, Returned: returned, Refunded: refunded, Left: sale.Total.Sub(refunded)}, nil
}

// Return devuelve lines de la venta saleID con el motivo, y la plata con
// payments (cualquier medio); by es quién. Devuelve la devolución guardada.
//   - service.ErrValidation: motivo, cantidades o pagos mal.
//   - service.ErrNotFound: la venta no existe.
//   - service.ErrConflict: la caja está cerrada o la venta está anulada.
func (s *Service) Return(ctx context.Context, by, saleID int64, reason string, lines []ReturnLine,
	payments []*model.Payment) (*model.SaleReturn, error) {
	reason, err := model.ValidateReturnReason(reason)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}
	if len(lines) == 0 {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, model.ErrReturnNoLines)
	}
	for _, p := range payments {
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
		}
	}
	ret := &model.SaleReturn{SaleID: saleID, Reason: reason, CreatedBy: by}
	err = s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		storeID, sess, err := lockSession(ctx, tx)
		if err != nil {
			return err
		}
		ret.CashierSessionID = sess.ID
		sale, err := postgres.NewSaleStore(tx).Lock(ctx, saleID)
		if err != nil {
			return err
		}
		if sale.Status == model.SaleVoided {
			return ErrReturnSaleVoided
		}
		view, err := s.returnView(ctx, tx, saleID)
		if err != nil {
			return err
		}
		items := map[int64]*model.SaleItem{}
		for _, it := range view.Sale.Items {
			items[it.ID] = it
		}
		comps, err := postgres.NewSaleReturnStore(tx).Components(ctx, saleID)
		if err != nil {
			return err
		}

		// Renglones: cantidades y plata.
		total := decimal.Zero
		seen := map[int64]bool{}
		for _, l := range lines {
			it, ok := items[l.SaleItemID]
			if !ok || seen[l.SaleItemID] {
				return ErrReturnLineUnknown
			}
			seen[l.SaleItemID] = true
			if !l.Quantity.IsPositive() || !l.Quantity.Equal(l.Quantity.Round(3)) {
				return fmt.Errorf("%w: %w", service.ErrValidation, model.ErrReturnQuantity)
			}
			if it.SaleType != model.SaleTypeWeight && !l.Quantity.IsInteger() {
				return fmt.Errorf("%w: %w", service.ErrValidation, model.ErrQuantityNotWhole)
			}
			if l.Quantity.GreaterThan(it.Quantity.Sub(view.Returned[it.ID])) {
				return &LineError{ProductID: it.ProductID, ProductName: it.ProductName, Err: ErrReturnTooMuch}
			}
			amount := l.Quantity.Mul(it.UnitPrice)
			if view.Sale.Subtotal.IsPositive() {
				amount = amount.Mul(view.Sale.Total).Div(view.Sale.Subtotal)
			}
			amount = amount.Round(2)
			ret.Items = append(ret.Items, &model.SaleReturnItem{SaleItemID: it.ID, Quantity: l.Quantity,
				Amount: amount, Restock: l.Restock, ProductName: it.ProductName})
			total = total.Add(amount)
		}
		// Nunca más de lo que queda por devolver (el prorrateo redondea).
		if total.GreaterThan(view.Left) {
			total = view.Left
		}
		ret.Total = total
		if !total.IsPositive() {
			return fmt.Errorf("%w: %w", service.ErrValidation, model.ErrReturnNoLines)
		}

		// La plata: suma justo el total; ACCOUNT, del cliente y hasta su deuda.
		paid, account := decimal.Zero, decimal.Zero
		for _, p := range payments {
			paid = paid.Add(p.Amount)
			if p.Method == model.PaymentAccount {
				account = account.Add(p.Amount)
			}
		}
		if !paid.Equal(total) {
			return ErrReturnPaymentsTotal
		}
		if account.IsPositive() {
			if view.Sale.CustomerID == nil {
				return ErrReturnAccount
			}
			cs := postgres.NewCustomerStore(tx)
			if _, err := cs.Lock(ctx, *view.Sale.CustomerID); err != nil {
				return err
			}
			debts, err := cs.Debts(ctx, []int64{*view.Sale.CustomerID})
			if err != nil {
				return err
			}
			if account.GreaterThan(debts[*view.Sale.CustomerID]) {
				return ErrReturnAccount
			}
		}
		ret.Payments = payments

		// Stock: lo que vuelve a la góndola, trabado en orden de id.
		type back struct {
			product   int64
			qty, cost decimal.Decimal
		}
		var backs []back
		for _, ri := range ret.Items {
			if !ri.Restock {
				continue
			}
			it := items[ri.SaleItemID]
			switch {
			case len(comps[it.ID]) > 0:
				for _, c := range comps[it.ID] {
					backs = append(backs, back{c.ProductID, c.Quantity.Div(it.Quantity).Mul(ri.Quantity).Round(3), c.UnitCost})
				}
			case it.SaleType == model.SaleTypeComposite:
				// combo sin detalle (no debería pasar): no hay qué devolver
			default:
				p, err := postgres.NewProductStore(tx).GetByID(ctx, it.ProductID)
				if err != nil {
					return err
				}
				if !p.OpenPrice { // un artículo sin registrar no tiene stock
					backs = append(backs, back{it.ProductID, ri.Quantity, it.UnitCost})
				}
			}
		}
		ids := make([]int64, 0, len(backs))
		for _, b := range backs {
			ids = append(ids, b.product)
		}
		slices.Sort(ids)
		balances := postgres.NewStockBalanceStore(tx)
		for _, id := range slices.Compact(ids) {
			if _, err := balances.LockOnHand(ctx, id); err != nil {
				return err
			}
		}
		movements := postgres.NewStockMovementStore(tx)
		for _, b := range backs {
			if !b.qty.IsPositive() {
				continue
			}
			if _, err := movements.Create(ctx, &model.StockMovement{
				ProductID: b.product, StoreID: storeID, MovementType: model.MovementReturn,
				Quantity: b.qty, UnitCost: b.cost, SaleID: &saleID, PerformedBy: &by,
			}); err != nil {
				return err
			}
			if _, err := balances.Add(ctx, b.product, storeID, b.qty); err != nil {
				return err
			}
		}

		if err := postgres.NewSaleReturnStore(tx).Create(ctx, ret); err != nil {
			return err
		}
		_, err = postgres.NewAuditLogStore(tx).Create(ctx, postgres.AuditEntry{
			UserID: &by, Table: "sale_returns", Action: "INSERT", RecordID: ret.ID,
			NewData: map[string]any{"sale_id": saleID, "sale_number": view.Sale.SaleNumber, "total": ret.Total,
				"reason": reason, "items": ret.Items, "payments": ret.Payments, "session_id": sess.ID},
		})
		return err
	})
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, translate(err)
	}
	return ret, nil
}
