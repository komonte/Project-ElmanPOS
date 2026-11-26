// Package adjustments es el caso de uso F-09b: las salidas conocidas de
// mercadería (consumo propio, rotura, vencido, otra), anotadas cualquier día,
// sin un conteo abierto. Cada salida resta stock con movimientos ADJUSTMENT
// que la nombran como origen.
//
// Decisiones (2026-11-26):
//   - Sale del único local activo (como compras y conteos).
//   - Solo productos activos y que no sean combos.
//   - No se puede sacar más de lo que hay en el local.
//   - De un producto con lotes: del lote elegido y, si no alcanza o no se
//     eligió, de los demás en orden FIFO (el que vence antes primero). Lo que
//     los lotes no cubren sale sin lote. Cada lote a su costo; sin lote, al
//     costo promedio. El promedio no cambia.
//   - No se edita ni se anula: un error se corrige con un conteo.
//   - Si el producto ya está contado en el conteo abierto, se avisa
//     (InOpenCount): una salida anotada después de contarlo está bien, pero
//     si salió antes de contarlo se descontaría dos veces.
//   - by es quién la anota (F-13): queda en created_by, en el performed_by
//     de los movimientos y en el user_id de la auditoría.
//
// Los errores salen como los del service genérico (service.ErrValidation,
// service.ErrConflict...), así la API y la web los traducen igual.
package adjustments

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

var (
	ErrUnknownProduct   = fmt.Errorf("product does not exist: %w", service.ErrValidation)
	ErrProductInactive  = fmt.Errorf("product is inactive: %w", service.ErrConflict)
	ErrCompositeProduct = fmt.Errorf("a composite product has no stock of its own: %w", service.ErrValidation)
	ErrBatchNotAllowed  = fmt.Errorf("this product does not control batches: %w", service.ErrValidation)
	ErrUnknownBatch     = fmt.Errorf("the batch is not one of this product's: %w", service.ErrValidation)
)

// NotEnoughStockError: la salida es mayor que el stock del local. Dice
// cuánto hay, para el mensaje. Cumple service.ErrConflict.
type NotEnoughStockError struct {
	Available decimal.Decimal
}

func (e *NotEnoughStockError) Error() string {
	return "not enough stock: there is " + e.Available.String()
}

func (e *NotEnoughStockError) Is(target error) bool { return target == service.ErrConflict }

// auditStore es lo que el caso de uso usa del registro de auditoría.
type auditStore interface {
	Create(ctx context.Context, e postgres.AuditEntry) (int64, error)
}

type Service struct {
	db postgres.DBTX     // lecturas sueltas
	tx postgres.TxRunner // Record: todo en una transacción

	// audit es un campo para que el test pueda simular que falla al final y
	// ver que la salida no deja nada.
	audit func(tx postgres.DBTX) auditStore
}

func New(db postgres.DBTX, tx postgres.TxRunner) *Service {
	return &Service{
		db:    db,
		tx:    tx,
		audit: func(tx postgres.DBTX) auditStore { return postgres.NewAuditLogStore(tx) },
	}
}

// Result es lo que devuelve Record.
type Result struct {
	Adjustment *model.StockAdjustment `json:"adjustment"`
	// InOpenCount: el producto ya está contado en el conteo abierto (aviso).
	InOpenCount bool `json:"in_open_count"`
}

// Record anota una salida de by: guarda la salida, sus movimientos (uno por lote,
// o uno sin lote), resta el saldo del local y deja la auditoría, todo junto.
// No modifica a.
//   - service.ErrValidation: la salida está mal (envuelve el error del
//     model), el producto no existe o es un combo, o el lote no corresponde.
//   - service.ErrConflict: el producto está inactivo, no hay stock suficiente
//     (*NotEnoughStockError) o no hay un único local activo.
func (s *Service) Record(ctx context.Context, by int64, a *model.StockAdjustment) (*Result, error) {
	if a == nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, model.ErrNilStockAdjustment)
	}
	in := *a
	in.CreatedBy = &by
	if err := in.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}

	var res Result
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		storeID, err := activeStore(ctx, tx)
		if err != nil {
			return err
		}
		prod, err := exitProduct(ctx, tx, in.ProductID)
		if err == nil {
			if werr := prod.CheckWholeQuantity(in.Quantity); werr != nil {
				err = fmt.Errorf("%w: %w", service.ErrValidation, werr)
			}
		}
		if err != nil {
			return err
		}
		if in.BatchID != nil && !prod.ControlsBatches {
			return ErrBatchNotAllowed
		}

		// Primero traba el stock y después lee saldo y costo (como compras y
		// conteos): otra salida, compra o confirmación del mismo producto
		// espera a esta.
		balances := postgres.NewStockBalanceStore(tx)
		if _, err := balances.LockOnHand(ctx, prod.ID); err != nil {
			return err
		}
		available, err := balances.OnHandAt(ctx, prod.ID, storeID)
		if err != nil {
			return err
		}
		if in.Quantity.GreaterThan(available) {
			return &NotEnoughStockError{Available: available}
		}
		avg := decimal.Zero
		if prev, err := postgres.NewCostStore(tx).Current(ctx, prod.ID); err == nil {
			avg = prev.AvgCost
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}

		in.StoreID = storeID
		saved, err := postgres.NewStockAdjustmentStore(tx).Create(ctx, &in)
		if err != nil {
			return err
		}
		if err := takeStock(ctx, tx, prod, saved, in.BatchID, avg); err != nil {
			return err
		}
		if _, err := balances.Add(ctx, prod.ID, storeID, in.Quantity.Neg()); err != nil {
			return err
		}

		saved.BatchID, saved.ProductName = in.BatchID, prod.Name
		if _, err := s.audit(tx).Create(ctx, postgres.AuditEntry{
			UserID:   &by,
			Table:    "stock_adjustments",
			Action:   "INSERT",
			RecordID: saved.ID,
			NewData:  saved,
		}); err != nil {
			return err
		}
		res.Adjustment = saved
		res.InOpenCount, err = inOpenCount(ctx, tx, storeID, prod.ID)
		return err
	})
	if err != nil {
		return nil, translate(err)
	}
	return &res, nil
}

// takeStock arma los movimientos de la salida: del lote elegido, después
// FIFO, y lo que los lotes no cubren sin lote al promedio.
func takeStock(ctx context.Context, tx postgres.DBTX, prod *model.Product, a *model.StockAdjustment,
	batchID *int64, avg decimal.Decimal) error {
	movements := postgres.NewStockMovementStore(tx)
	move := func(batch *int64, qty, unitCost decimal.Decimal) error {
		_, err := movements.Create(ctx, &model.StockMovement{
			ProductID:    prod.ID,
			StoreID:      a.StoreID,
			BatchID:      batch,
			MovementType: model.MovementAdjustment,
			Reason:       a.Reason,
			Quantity:     qty.Neg(),
			UnitCost:     unitCost,
			AdjustmentID: &a.ID,
			PerformedBy:  a.CreatedBy,
		})
		return err
	}

	left := a.Quantity
	if prod.ControlsBatches {
		batches := postgres.NewBatchStore(tx)
		var takes []model.BatchTake
		if batchID != nil {
			take, rest, err := batches.TakeFrom(ctx, prod.ID, *batchID, left)
			if errors.Is(err, store.ErrNotFound) {
				return ErrUnknownBatch
			}
			if err != nil {
				return err
			}
			if take != nil {
				takes = append(takes, *take)
			}
			left = rest
		}
		if left.IsPositive() {
			more, rest, err := batches.TakeFIFO(ctx, prod.ID, left)
			if err != nil {
				return err
			}
			takes = append(takes, more...)
			left = rest
		}
		for _, t := range takes {
			if err := move(&t.BatchID, t.Quantity, t.UnitCost); err != nil {
				return err
			}
		}
	}
	if left.IsPositive() {
		return move(nil, left, avg)
	}
	return nil
}

// Recent devuelve las últimas salidas del local activo (la más nueva
// primero), hasta limit.
func (s *Service) Recent(ctx context.Context, limit int) ([]*model.StockAdjustment, error) {
	storeID, err := activeStore(ctx, s.db)
	if err != nil {
		return nil, err
	}
	return postgres.NewStockAdjustmentStore(s.db).Recent(ctx, storeID, limit)
}

// Batches devuelve los lotes con mercadería de un producto (FIFO), para
// elegir de cuál sale. Vacía si no controla lotes o no tiene.
func (s *Service) Batches(ctx context.Context, productID int64) ([]*model.ProductBatch, error) {
	if productID <= 0 {
		return nil, service.ErrInvalidID
	}
	return postgres.NewBatchStore(s.db).OpenBatches(ctx, productID)
}

// InOpenCount dice si el producto ya está contado en el conteo abierto del
// local activo: la pantalla lo avisa antes de anotar la salida.
func (s *Service) InOpenCount(ctx context.Context, productID int64) (bool, error) {
	storeID, err := activeStore(ctx, s.db)
	if err != nil {
		return false, err
	}
	return inOpenCount(ctx, s.db, storeID, productID)
}

func inOpenCount(ctx context.Context, db postgres.DBTX, storeID, productID int64) (bool, error) {
	counts := postgres.NewStockCountStore(db)
	c, err := counts.Draft(ctx, storeID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, err = counts.Item(ctx, c.ID, productID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// activeStore es el local del que sale: el único activo.
func activeStore(ctx context.Context, db postgres.DBTX) (int64, error) {
	id, err := postgres.NewStoreStore(db).ActiveID(ctx)
	if err != nil {
		return 0, translate(err)
	}
	return id, nil
}

// exitProduct trae el producto y revisa que pueda tener salidas: que exista,
// esté activo y no sea un combo.
func exitProduct(ctx context.Context, tx postgres.DBTX, id int64) (*model.Product, error) {
	prod, err := postgres.NewProductStore(tx).GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrUnknownProduct
		}
		return nil, err
	}
	switch {
	case !prod.IsActive:
		return nil, ErrProductInactive
	case prod.SaleType == model.SaleTypeComposite:
		return nil, ErrCompositeProduct
	}
	return prod, nil
}

// translate pasa los errores del store a los del service, como en conteos.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, service.ErrValidation), errors.Is(err, service.ErrConflict):
		return err
	case errors.Is(err, store.ErrNotFound):
		return service.ErrNotFound
	case errors.Is(err, store.ErrConflict):
		return fmt.Errorf("%w: %w", service.ErrConflict, err)
	}
	return err
}
