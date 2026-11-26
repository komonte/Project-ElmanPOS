// Package stockcounts es el caso de uso F-09: el conteo físico de stock, que
// también sirve de inventario inicial (el stock que ya está en el local no se
// carga como compra: se cuenta).
//
// Decisiones (2026-11-25):
//   - Un solo conteo en borrador por local; se cuenta en el único local
//     activo (sin local activo, o con varios, se rechaza).
//   - Se cuenta por sectores: cada carga dice dónde ("Góndola 3",
//     "Depósito"). El mismo producto en dos sectores se suma.
//   - La foto del sistema (system_quantity) se saca la primera vez que se
//     carga el producto en el conteo. Al confirmar se mueve la diferencia
//     contra esa foto, no se pisa el saldo: si mientras tanto entró una
//     compra, no se pierde.
//   - Lo que no se contó no se toca (no se pone en 0): se puede contar de a
//     partes.
//   - Costo y vencimiento son opcionales por producto y solo se usan si
//     sobra mercadería.
//   - Descartar un borrador no deja rastro; confirmar es definitivo.
//   - Sin login todavía: created_by queda NULL (0015).
//
// Los errores salen como los del service genérico (service.ErrValidation,
// service.ErrConflict...), así la API y la web los traducen igual.
package stockcounts

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
	ErrProductInactive  = fmt.Errorf("product is inactive: activate it before counting it: %w", service.ErrConflict)
	ErrCompositeProduct = fmt.Errorf("a composite product has no stock of its own: count its components: %w", service.ErrValidation)
	ErrExpiryNotAllowed = fmt.Errorf("this product does not control batches: leave the expiry empty: %w", service.ErrValidation)

	// Al confirmar: la diferencia dejaría el saldo del local en negativo (el
	// stock bajó por otro lado después de la foto). Se recuenta el producto.
	ErrNegativeStock = fmt.Errorf("the adjustment would leave negative stock: count the product again: %w", service.ErrConflict)
)

// auditStore es lo que el caso de uso usa del registro de auditoría.
type auditStore interface {
	Create(ctx context.Context, e postgres.AuditEntry) (int64, error)
}

type Service struct {
	db postgres.DBTX     // lecturas sueltas
	tx postgres.TxRunner // todo lo que escribe

	// audit es un campo para que el test pueda simular que falla al final y
	// ver que la confirmación no deja nada.
	audit func(tx postgres.DBTX) auditStore
}

func New(db postgres.DBTX, tx postgres.TxRunner) *Service {
	return &Service{
		db:    db,
		tx:    tx,
		audit: func(tx postgres.DBTX) auditStore { return postgres.NewAuditLogStore(tx) },
	}
}

// Open devuelve el conteo en borrador del local activo, con sus renglones, y
// si no hay ninguno lo abre. Si dos lo abren a la vez, los dos siguen el
// mismo.
func (s *Service) Open(ctx context.Context) (*model.StockCount, error) {
	var id int64
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		storeID, err := activeStore(ctx, tx)
		if err != nil {
			return err
		}
		counts := postgres.NewStockCountStore(tx)
		c, err := counts.Draft(ctx, storeID)
		if errors.Is(err, store.ErrNotFound) {
			c, err = counts.CreateDraft(ctx, storeID)
		}
		if err != nil {
			return err
		}
		id = c.ID
		return nil
	})
	if errors.Is(err, store.ErrCountDraftExists) {
		// Otro lo abrió entre que miramos y creamos: se sigue ese.
		return s.Current(ctx)
	}
	if err != nil {
		return nil, translate(err)
	}
	return s.Get(ctx, id)
}

// Current devuelve el conteo en borrador del local activo con sus renglones.
// service.ErrNotFound si no hay ninguno abierto.
func (s *Service) Current(ctx context.Context) (*model.StockCount, error) {
	storeID, err := activeStore(ctx, s.db)
	if err != nil {
		return nil, err
	}
	c, err := postgres.NewStockCountStore(s.db).Draft(ctx, storeID)
	if err != nil {
		return nil, translate(err)
	}
	return s.Get(ctx, c.ID)
}

// Get devuelve un conteo con sus renglones y cargas.
//   - service.ErrInvalidID: id <= 0.
//   - service.ErrNotFound: no existe.
func (s *Service) Get(ctx context.Context, id int64) (*model.StockCount, error) {
	if id <= 0 {
		return nil, service.ErrInvalidID
	}
	c, err := postgres.NewStockCountStore(s.db).GetByID(ctx, id)
	if err != nil {
		return nil, translate(err)
	}
	return c, nil
}

// Sectors devuelve los sectores ya usados, para sugerirlos.
func (s *Service) Sectors(ctx context.Context) ([]string, error) {
	return postgres.NewStockCountStore(s.db).Sectors(ctx)
}

// AddEntry carga una cantidad de un producto en un sector del conteo y
// devuelve el renglón del producto con el total contado. La primera carga
// del producto crea el renglón con la foto del saldo del local.
//   - service.ErrValidation: la carga está mal (envuelve el error del model),
//     o el producto no existe o es un pack.
//   - service.ErrConflict: el producto está inactivo, o el conteo ya está
//     confirmado.
//   - service.ErrNotFound: el conteo no existe.
func (s *Service) AddEntry(ctx context.Context, countID int64, e *model.StockCountEntry) (*model.StockCountItem, error) {
	if countID <= 0 {
		return nil, service.ErrInvalidID
	}
	if e == nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, model.ErrNilStockCountEntry)
	}
	in := *e // Validate normaliza el sector: no se toca lo que mandó quien llama
	if err := in.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}

	var item *model.StockCountItem
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		counts := postgres.NewStockCountStore(tx)
		c, err := counts.LockDraft(ctx, countID)
		if err != nil {
			return err
		}
		if _, err := countableProduct(ctx, tx, in.ProductID); err != nil {
			return err
		}
		item, err = addEntry(ctx, tx, c, &in)
		return err
	})
	if err != nil {
		return nil, translate(err)
	}
	return item, nil
}

// addEntry guarda una carga ya validada en un conteo trabado (LockDraft) y
// devuelve el renglón del producto con el total. La primera carga del
// producto crea el renglón con la foto del saldo del local. Lo usan la carga
// a mano y la importación.
func addEntry(ctx context.Context, tx postgres.DBTX, c *model.StockCount, in *model.StockCountEntry) (*model.StockCountItem, error) {
	counts := postgres.NewStockCountStore(tx)
	it, err := counts.Item(ctx, c.ID, in.ProductID)
	if errors.Is(err, store.ErrNotFound) {
		// Primera carga del producto: la foto del saldo. El conteo está
		// trabado, así que nadie más crea este renglón a la vez.
		var onHand decimal.Decimal
		onHand, err = postgres.NewStockBalanceStore(tx).OnHandAt(ctx, in.ProductID, c.StoreID)
		if err != nil {
			return nil, err
		}
		it, err = counts.CreateItem(ctx, c.ID, in.ProductID, onHand)
	}
	if err != nil {
		return nil, err
	}
	if _, err := counts.AddEntry(ctx, it.ID, in.Sector, in.Quantity); err != nil {
		return nil, err
	}
	return counts.Item(ctx, c.ID, in.ProductID)
}

// RemoveEntry quita una carga del conteo. Si era la última del producto, el
// producto vuelve a "no contado".
//   - service.ErrNotFound: el conteo o la carga no existen (o la carga es de
//     otro conteo).
//   - service.ErrConflict: el conteo ya está confirmado.
func (s *Service) RemoveEntry(ctx context.Context, countID, entryID int64) error {
	if countID <= 0 || entryID <= 0 {
		return service.ErrInvalidID
	}
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		counts := postgres.NewStockCountStore(tx)
		if _, err := counts.LockDraft(ctx, countID); err != nil {
			return err
		}
		_, err := counts.DeleteEntry(ctx, countID, entryID)
		return err
	})
	return translate(err)
}

// SetDetails guarda el costo (nil = sin costo) y el vencimiento (vacío = sin
// vencimiento) de un producto contado.
//   - service.ErrValidation: costo o vencimiento mal escritos, o vencimiento
//     en un producto que no controla lotes.
//   - service.ErrNotFound: el conteo no existe o el producto no se contó.
//   - service.ErrConflict: el conteo ya está confirmado.
func (s *Service) SetDetails(ctx context.Context, countID, productID int64, unitCost *decimal.Decimal, expiry string) error {
	if countID <= 0 || productID <= 0 {
		return service.ErrInvalidID
	}
	in := &model.StockCountItem{UnitCost: unitCost, ExpiryDate: expiry}
	if err := in.Validate(); err != nil {
		return fmt.Errorf("%w: %w", service.ErrValidation, err)
	}

	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		counts := postgres.NewStockCountStore(tx)
		if _, err := counts.LockDraft(ctx, countID); err != nil {
			return err
		}
		it, err := counts.Item(ctx, countID, productID)
		if err != nil {
			return err
		}
		if in.ExpiryDate != "" {
			prod, err := postgres.NewProductStore(tx).GetByID(ctx, productID)
			if err != nil {
				return err
			}
			if !prod.ControlsBatches {
				return ErrExpiryNotAllowed
			}
		}
		return counts.SetItemDetails(ctx, countID, it.ID, in.UnitCost, in.ExpiryDate)
	})
	return translate(err)
}

// Discard descarta un conteo en borrador con todo lo cargado. No dejó
// movimientos: el stock queda como estaba.
//   - service.ErrNotFound: no existe.
//   - service.ErrConflict: ya está confirmado (no se borra nunca).
func (s *Service) Discard(ctx context.Context, countID int64) error {
	if countID <= 0 {
		return service.ErrInvalidID
	}
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		counts := postgres.NewStockCountStore(tx)
		if _, err := counts.LockDraft(ctx, countID); err != nil {
			return err
		}
		return counts.DeleteDraft(ctx, countID)
	})
	return translate(err)
}

// AutoBatchNumber es el lote que recibe la mercadería que sobra en un conteo
// de un producto que controla lotes: "I-" (inventario) y el número de conteo.
// En el inventario inicial es el lote de todo lo que ya estaba en el local.
func AutoBatchNumber(countID int64) string {
	return fmt.Sprintf("I-%d", countID)
}

// Result es lo que devuelve Confirm.
type Result struct {
	Count *model.StockCount `json:"count"` // ya confirmado, con sus renglones
	// Adjusted: cuántos productos tuvieron diferencia (y movimiento). Los
	// demás coincidían con el sistema.
	Adjusted int `json:"adjusted"`
}

// Confirm confirma un conteo en borrador: por cada producto contado cuya
// cantidad difiere de la foto, un movimiento ADJUSTMENT/COUNT con la
// diferencia, y el saldo del local se mueve lo mismo. Todo en una
// transacción, con auditoría; si algo falla no queda nada.
//
// Si sobra mercadería (diferencia positiva):
//   - Con costo escrito, el movimiento va a ese costo y el costo entra al
//     promedio (F-04), igual que una compra. Sin costo, va al promedio
//     vigente (0 si el producto nunca tuvo costo) y el promedio no cambia.
//   - Si el producto controla lotes, lo que sobra va a un lote nuevo
//     (AutoBatchNumber) con el vencimiento escrito.
//
// Si falta (diferencia negativa):
//   - Si el producto controla lotes, se descuenta de los lotes, primero el
//     que vence antes; cada lote es un movimiento a su costo. Lo que los
//     lotes no cubren va sin lote, al promedio vigente.
//   - Si no, un movimiento al promedio vigente.
//
// El costo y el vencimiento de un producto sin diferencia, o al que le falta
// mercadería, no se usan.
//
//   - service.ErrNotFound: el conteo no existe.
//   - service.ErrConflict: ya está confirmado, o un ajuste dejaría el saldo
//     negativo (ErrNegativeStock, dice el producto).
func (s *Service) Confirm(ctx context.Context, countID int64) (*Result, error) {
	if countID <= 0 {
		return nil, service.ErrInvalidID
	}
	var res Result
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		counts := postgres.NewStockCountStore(tx)
		if _, err := counts.LockDraft(ctx, countID); err != nil {
			return err
		}
		c, err := counts.GetByID(ctx, countID)
		if err != nil {
			return err
		}
		for _, it := range c.Items {
			if it.Difference().IsZero() {
				continue
			}
			if err := adjust(ctx, tx, c, it); err != nil {
				return fmt.Errorf("%s: %w", it.ProductName, err)
			}
			res.Adjusted++
		}

		confirmed, err := counts.Confirm(ctx, countID)
		if err != nil {
			return err
		}
		confirmed.Items = c.Items
		_, err = s.audit(tx).Create(ctx, postgres.AuditEntry{
			Table:    "stock_counts",
			Action:   "UPDATE",
			RecordID: countID,
			NewData:  confirmed,
		})
		if err != nil {
			return err
		}
		res.Count = confirmed
		return nil
	})
	if err != nil {
		return nil, translate(err)
	}
	return &res, nil
}

// adjust mueve el stock de un producto del conteo por su diferencia.
func adjust(ctx context.Context, tx postgres.DBTX, c *model.StockCount, it *model.StockCountItem) error {
	diff := it.Difference()

	// Primero traba el stock del producto y después lee costo y saldo (igual
	// que la recepción): una compra del mismo producto espera a este ajuste.
	balances := postgres.NewStockBalanceStore(tx)
	onHand, err := balances.LockOnHand(ctx, it.ProductID)
	if err != nil {
		return err
	}
	atStore, err := balances.OnHandAt(ctx, it.ProductID, c.StoreID)
	if err != nil {
		return err
	}
	if atStore.Add(diff).IsNegative() {
		return ErrNegativeStock
	}
	prod, err := postgres.NewProductStore(tx).GetByID(ctx, it.ProductID)
	if err != nil {
		return err
	}
	costStore := postgres.NewCostStore(tx)
	prev, err := costStore.Current(ctx, it.ProductID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	avg := decimal.Zero
	if prev != nil {
		avg = prev.AvgCost
	}

	movements := postgres.NewStockMovementStore(tx)
	move := func(batchID *int64, qty, unitCost decimal.Decimal) error {
		_, err := movements.Create(ctx, &model.StockMovement{
			ProductID:    it.ProductID,
			StoreID:      c.StoreID,
			BatchID:      batchID,
			MovementType: model.MovementAdjustment,
			Reason:       model.ReasonCount,
			Quantity:     qty,
			UnitCost:     unitCost,
			CountID:      &c.ID,
		})
		return err
	}

	if diff.IsPositive() {
		unitCost := avg
		if it.UnitCost != nil {
			unitCost = *it.UnitCost
		}
		var batchID *int64
		if prod.ControlsBatches {
			b, err := postgres.NewBatchStore(tx).Create(ctx, &model.ProductBatch{
				ProductID:        it.ProductID,
				BatchNumber:      AutoBatchNumber(c.ID),
				ExpiryDate:       it.ExpiryDate,
				OriginalQuantity: diff,
				UnitCost:         unitCost,
			})
			if err != nil {
				return err
			}
			batchID = &b.ID
		}
		if err := move(batchID, diff, unitCost); err != nil {
			return err
		}
		if it.UnitCost != nil {
			// Promedio contra todo el stock de antes del ajuste (onHand).
			if _, err := costStore.Create(ctx, &model.Cost{
				ProductID: it.ProductID,
				UnitCost:  *it.UnitCost,
				AvgCost:   model.AverageCost(prev, onHand, *it.UnitCost, diff),
			}); err != nil {
				return err
			}
		}
	} else {
		left := diff.Neg()
		if prod.ControlsBatches {
			var takes []model.BatchTake
			takes, left, err = postgres.NewBatchStore(tx).TakeFIFO(ctx, it.ProductID, left)
			if err != nil {
				return err
			}
			for _, t := range takes {
				if err := move(&t.BatchID, t.Quantity.Neg(), t.UnitCost); err != nil {
					return err
				}
			}
		}
		if left.IsPositive() {
			if err := move(nil, left.Neg(), avg); err != nil {
				return err
			}
		}
	}

	_, err = balances.Add(ctx, it.ProductID, c.StoreID, diff)
	return err
}

// activeStore es el local donde se cuenta: el único activo.
func activeStore(ctx context.Context, db postgres.DBTX) (int64, error) {
	id, err := postgres.NewStoreStore(db).ActiveID(ctx)
	if err != nil {
		return 0, translate(err)
	}
	return id, nil
}

// countableProduct trae el producto y revisa que se pueda contar: que exista,
// esté activo y no sea un pack (no tiene stock propio).
func countableProduct(ctx context.Context, tx postgres.DBTX, id int64) (*model.Product, error) {
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

// translate pasa los errores del store a los del service. Los de regla
// (ErrCountNotDraft, ErrNoActiveStore...) cumplen store.ErrConflict y se
// envuelven en service.ErrConflict, así la API responde 409 y el mensaje dice
// cuál fue.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, service.ErrValidation), errors.Is(err, service.ErrConflict):
		return err // ya traducido (los errores de este paquete)
	case errors.Is(err, store.ErrNotFound):
		return service.ErrNotFound
	case errors.Is(err, store.ErrConflict):
		return fmt.Errorf("%w: %w", service.ErrConflict, err)
	}
	return err
}
