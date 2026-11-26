package sessions

import (
	"context"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Cierre de caja con arqueo (F-12). Decisiones (2026-11-26):
//   - Cierre ciego: se declara lo contado sin ver lo esperado. Por eso el
//     resumen (Summary) solo existe para una sesión cerrada.
//   - Se declara un monto por medio; el que se deja vacío es 0 y se guarda
//     igual (las 5 filas de cash_audits): el arqueo queda completo.
//   - Lo esperado: efectivo inicial + pagos en efectivo; los otros medios,
//     sus pagos. Solo ventas COMPLETED. Como solo se anulan ventas de la
//     sesión abierta (F-08), el arqueo de una sesión cerrada no cambia.
//   - Cada cierre se audita, haya diferencia o no (NFR-03).
//   - Sin login todavía: el cierre no guarda quién cerró.

// ErrSessionStillOpen: el resumen de una sesión abierta mostraría lo esperado
// antes de contar (cierre ciego).
var ErrSessionStillOpen = fmt.Errorf("the session is still open: %w", service.ErrConflict)

// Summary es el cierre de una sesión: la sesión, el arqueo por medio y
// cuántas ventas tuvo.
type Summary struct {
	Session *model.CashierSession `json:"session"`
	Close   *model.CloseSummary   `json:"close"`
	// Sales y Voided cuentan las ventas cobradas y las anuladas; Total es lo
	// vendido (solo las cobradas).
	Sales  int             `json:"sales"`
	Voided int             `json:"voided"`
	Total  decimal.Decimal `json:"total"`
}

// Close cierra la sesión sessionID con lo declarado por medio y devuelve el
// resumen. Se pasa la sesión (la del formulario) y no "la abierta": si
// mientras se contaba alguien la cerró y abrió otra, no se cierra la nueva.
//   - service.ErrValidation: un monto declarado está mal o un medio está dos
//     veces (envuelve el error del model).
//   - service.ErrConflict: la sesión no existe o ya está cerrada
//     (store.ErrSessionNotOpen adentro).
//
// Una transacción: trabar la sesión (una venta a medio cobrar termina antes,
// y después ninguna entra), calcular lo esperado, guardar lo declarado,
// cerrar y auditar.
func (s *Service) Close(ctx context.Context, sessionID int64, declared []*model.CashAudit) (*Summary, error) {
	var sum *Summary
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		sessions := postgres.NewCashierSessionStore(tx)
		if _, err := sessions.LockOpen(ctx, sessionID); err != nil {
			return err
		}
		var err error
		sum, err = summary(ctx, tx, sessionID, declared)
		if err != nil {
			return err
		}
		audits := postgres.NewCashAuditStore(tx)
		for _, a := range sum.Close.Audits(sessionID) {
			if err := audits.Create(ctx, a); err != nil {
				return err
			}
		}
		closed, err := sessions.Close(ctx, sessionID, sum.Close.Line(model.PaymentCash).Declared, nil)
		if err != nil {
			return err
		}
		sum.Session = closed

		_, err = postgres.NewAuditLogStore(tx).Create(ctx, postgres.AuditEntry{
			Table:    "cashier_sessions",
			Action:   "CLOSE",
			RecordID: sessionID,
			NewData:  auditData(sum),
		})
		return err
	})
	if err != nil {
		return nil, translate(err)
	}
	return sum, nil
}

// Summary devuelve el cierre de una sesión cerrada (lo declarado se lee de
// cash_audits).
//   - service.ErrNotFound: la sesión no existe.
//   - ErrSessionStillOpen (service.ErrConflict): está abierta.
func (s *Service) Summary(ctx context.Context, sessionID int64) (*Summary, error) {
	if sessionID <= 0 {
		return nil, service.ErrInvalidID
	}
	sess, err := postgres.NewCashierSessionStore(s.db).Get(ctx, sessionID)
	if err != nil {
		return nil, translate(err)
	}
	if sess.Status != model.SessionClosed {
		return nil, ErrSessionStillOpen
	}
	declared, err := postgres.NewCashAuditStore(s.db).ListBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	sum, err := summary(ctx, s.db, sessionID, declared)
	if err != nil {
		return nil, translate(err)
	}
	sum.Session = sess
	return sum, nil
}

// Sales devuelve las ventas de la sesión, la última primero, con sus pagos
// (sin renglones) y las anuladas con su motivo: la lista de /cash, desde
// donde se reimprime el ticket o se anula (F-08). Vacía si no tiene.
func (s *Service) Sales(ctx context.Context, sessionID int64) ([]*model.Sale, error) {
	if sessionID <= 0 {
		return nil, service.ErrInvalidID
	}
	return postgres.NewSaleStore(s.db).ListBySession(ctx, sessionID)
}

// PaymentCounts cuenta los cobros de la sesión por medio (solo ventas
// COMPLETED), sin montos: al cerrar, ayuda a buscarlos en el home banking o
// en el cierre del posnet ("hoy hubo 3 transferencias") sin mostrar lo
// esperado (cierre ciego). Un medio sin cobros no está en el map.
func (s *Service) PaymentCounts(ctx context.Context, sessionID int64) (map[string]int, error) {
	list, err := postgres.NewSaleStore(s.db).ListBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	counts := make(map[string]int)
	for _, sale := range list {
		if sale.Status != model.SaleCompleted {
			continue
		}
		for _, p := range sale.Payments {
			counts[p.Method]++
		}
	}
	return counts, nil
}

// summary arma el arqueo de la sesión con lo declarado, y cuenta sus ventas.
// Sin Session: quien llama la pone.
func summary(ctx context.Context, db postgres.DBTX, sessionID int64, declared []*model.CashAudit) (*Summary, error) {
	sess, err := postgres.NewCashierSessionStore(db).Get(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	sales := postgres.NewSaleStore(db)
	paid, err := sales.PaidByMethod(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	audit, err := model.NewCloseSummary(sess.OpeningAmount, paid, declared)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}
	list, err := sales.ListBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	sum := &Summary{Close: audit, Total: decimal.Zero}
	for _, sale := range list {
		if sale.Status == model.SaleVoided {
			sum.Voided++
			continue
		}
		sum.Sales++
		sum.Total = sum.Total.Add(sale.Total)
	}
	return sum, nil
}

// auditData es lo que queda en audit_log de un cierre: la sesión, cuántas
// ventas tuvo y, por medio, lo esperado, lo declarado y la diferencia.
func auditData(sum *Summary) any {
	type line struct {
		Method     string          `json:"method"`
		Expected   decimal.Decimal `json:"expected"`
		Declared   decimal.Decimal `json:"declared"`
		Difference decimal.Decimal `json:"difference"`
	}
	lines := make([]line, 0, len(sum.Close.Lines))
	for _, l := range sum.Close.Lines {
		lines = append(lines, line{l.Method, l.Expected, l.Declared, l.Difference()})
	}
	return map[string]any{
		"session":        sum.Session,
		"sales":          sum.Sales,
		"voided":         sum.Voided,
		"total":          sum.Total,
		"lines":          lines,
		"has_difference": sum.Close.HasDifference(),
	}
}
