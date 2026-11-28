package sales

import (
	"context"
	"fmt"
	"slices"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Anular una venta entera (F-08). Decisiones (2026-11-26):
//   - Solo ventas de la sesión abierta: la plata se devuelve de esta caja, y
//     el arqueo de una sesión cerrada no cambia nunca. Anular ventas de otro
//     día queda para cuando haya devoluciones con su propio movimiento de
//     caja.
//   - El motivo es obligatorio (hasta 300 caracteres) y queda en la venta y
//     en audit_log.
//   - El stock vuelve exactamente por donde salió: un RETURN por cada SALE de
//     la venta, con el mismo lote y costo (lo que salió sin lote vuelve sin
//     lote). El costo promedio no cambia, como al vender.
//   - Solo el admin anula (permiso void de F-13, en la ruta). by es quién:
//     queda en el performed_by de los RETURN y en el user_id de la
//     auditoría.

var (
	// ErrSaleOtherSession: la venta es de una sesión ya cerrada.
	ErrSaleOtherSession = fmt.Errorf("only sales of the open session can be voided: %w", service.ErrConflict)
	// ErrSaleAlreadyVoided: la venta ya estaba anulada.
	ErrSaleAlreadyVoided = fmt.Errorf("%w: %w", service.ErrConflict, model.ErrSaleAlreadyVoided)
)

// Void anula la venta saleID con el motivo y la devuelve anulada (como
// Get: con renglones, pagos, fecha y motivo).
//   - service.ErrValidation: el motivo está vacío o es muy largo.
//   - service.ErrNotFound: la venta no existe.
//   - service.ErrConflict: la caja está cerrada (ErrRegisterClosed), la
//     venta es de otra sesión (ErrSaleOtherSession) o ya está anulada
//     (ErrSaleAlreadyVoided).
//
// Una transacción: trabar la sesión abierta (un cierre espera a que termine,
// y así el arqueo ya no la cuenta), la venta y el stock de sus productos en
// orden de id; devolver cada movimiento a su lote y al saldo; marcarla
// anulada y auditar.
func (s *Service) Void(ctx context.Context, by, saleID int64, reason string) (*model.Sale, error) {
	if saleID <= 0 {
		return nil, service.ErrInvalidID
	}
	reason, err := model.ValidateVoidReason(reason)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}

	var voided *model.Sale
	err = s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		_, sess, err := lockSession(ctx, tx)
		if err != nil {
			return err
		}
		sales := postgres.NewSaleStore(tx)
		sale, err := sales.Lock(ctx, saleID)
		if err != nil {
			return err
		}
		if sale.CashierSessionID != sess.ID {
			return ErrSaleOtherSession
		}
		if err := sale.CanVoid(); err != nil {
			return ErrSaleAlreadyVoided
		}
		// Con devoluciones, anular devolvería dos veces lo ya devuelto.
		if has, err := postgres.NewSaleReturnStore(tx).HasReturns(ctx, saleID); err != nil {
			return err
		} else if has {
			return ErrSaleHasReturns
		}

		movements, err := postgres.NewStockMovementStore(tx).SaleMovements(ctx, saleID)
		if err != nil {
			return err
		}
		if err := lockProducts(ctx, tx, movements); err != nil {
			return err
		}
		if err := giveBack(ctx, tx, by, movements); err != nil {
			return err
		}

		if _, err := sales.Void(ctx, saleID, reason); err != nil {
			return err
		}
		voided, err = sales.Get(ctx, saleID)
		if err != nil {
			return err
		}
		_, err = postgres.NewAuditLogStore(tx).Create(ctx, postgres.AuditEntry{
			UserID:   &by,
			Table:    "sales",
			Action:   "VOID",
			RecordID: saleID,
			NewData: map[string]any{
				"sale_number": voided.SaleNumber,
				"session_id":  voided.CashierSessionID,
				"total":       voided.Total,
				"payments":    voided.Payments,
				"reason":      voided.VoidReason,
				"returned":    len(movements),
			},
		})
		return err
	})
	if err != nil {
		return nil, translate(err)
	}
	return voided, nil
}

// lockProducts traba el stock de los productos de los movimientos, cada uno
// una vez y en orden de id, como al vender (§6: sin deadlocks).
func lockProducts(ctx context.Context, tx postgres.DBTX, movements []*model.StockMovement) error {
	ids := make([]int64, 0, len(movements))
	for _, m := range movements {
		ids = append(ids, m.ProductID)
	}
	slices.Sort(ids)
	balances := postgres.NewStockBalanceStore(tx)
	for _, id := range slices.Compact(ids) {
		if _, err := balances.LockOnHand(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

// giveBack devuelve cada salida: un RETURN positivo con el mismo producto,
// local, lote y costo, y la cantidad vuelve al lote (si tenía) y al saldo.
// by es quién anula.
func giveBack(ctx context.Context, tx postgres.DBTX, by int64, movements []*model.StockMovement) error {
	moves := postgres.NewStockMovementStore(tx)
	batches := postgres.NewBatchStore(tx)
	balances := postgres.NewStockBalanceStore(tx)
	for _, m := range movements {
		qty := m.Quantity.Neg() // la salida es negativa
		if _, err := moves.Create(ctx, &model.StockMovement{
			ProductID:    m.ProductID,
			StoreID:      m.StoreID,
			BatchID:      m.BatchID,
			MovementType: model.MovementReturn,
			Quantity:     qty,
			UnitCost:     m.UnitCost,
			SaleID:       m.SaleID,
			PerformedBy:  &by,
		}); err != nil {
			return err
		}
		if m.BatchID != nil {
			if _, err := batches.Restore(ctx, *m.BatchID, qty); err != nil {
				return err
			}
		}
		if _, err := balances.Add(ctx, m.ProductID, m.StoreID, qty); err != nil {
			return err
		}
	}
	return nil
}
