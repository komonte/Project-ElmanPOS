// Package purchases es el caso de uso F-06: la recepción de compras. Una
// compra entra entera o nada, en una transacción: cabecera, renglones, lotes,
// movimientos de stock, saldos, costos (con su promedio, F-04) y auditoría.
//
// Decisiones (2026-11-25):
//   - El stock entra al único local activo (sin local activo, o con varios,
//     se rechaza).
//   - Los costos van sin IVA; el total escrito es el subtotal sin IVA de la
//     factura. Si no coincide con la suma de los renglones es un aviso, no un
//     error (Result.TotalDifference).
//   - Dos renglones con el mismo lote, vencimiento y costo se juntan en un
//     lote; con distinto costo quedan dos lotes con el mismo número.
//   - Sin login todavía: received_by y performed_by quedan NULL (0013).
//
// Los errores salen como los del service genérico (service.ErrValidation,
// service.ErrConflict...), así la API y la web los traducen igual. Los de un
// renglón dicen cuál ("line 2: ...").
package purchases

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/costs"
)

var (
	// Del proveedor.
	ErrUnknownSupplier  = fmt.Errorf("supplier does not exist: %w", service.ErrValidation)
	ErrSupplierInactive = fmt.Errorf("supplier is inactive: %w", service.ErrConflict)

	// De un renglón (van envueltos con "line N: ").
	ErrUnknownProduct   = fmt.Errorf("product does not exist: %w", service.ErrValidation)
	ErrProductInactive  = fmt.Errorf("product is inactive: %w", service.ErrConflict)
	ErrCompositeProduct = fmt.Errorf("a composite product has no stock of its own: receive its components: %w", service.ErrValidation)
	ErrBatchRequired    = fmt.Errorf("this product controls batches: the batch number is required: %w", service.ErrValidation)
	ErrBatchNotAllowed  = fmt.Errorf("this product does not control batches: leave batch and expiry empty: %w", service.ErrValidation)
)

// Suggestion es lo que la pantalla muestra después de recibir, por producto:
// el costo nuevo, el promedio, el precio sugerido (con IVA) y el precio que
// tiene hoy, para decidir si se cambia. Nada de esto cambia el precio solo.
type Suggestion struct {
	ProductID      int64            `json:"product_id"`
	ProductName    string           `json:"product_name"`
	UnitCost       decimal.Decimal  `json:"unit_cost"`
	AvgCost        decimal.Decimal  `json:"avg_cost"`
	SuggestedPrice *decimal.Decimal `json:"suggested_price"` // nil con margen 0
	CurrentPrice   *decimal.Decimal `json:"current_price"`   // nil si no tiene
}

// Result es lo que devuelve Receive.
type Result struct {
	Purchase *model.Purchase `json:"purchase"`
	// TotalDifference: total escrito menos la suma de los renglones (0 si
	// coinciden). Se guarda igual; la pantalla avisa.
	TotalDifference decimal.Decimal `json:"total_difference"`
	// Suggestions: uno por producto, en el orden en que aparecen. Si un
	// producto está en dos renglones, vale el costo del último.
	Suggestions []Suggestion `json:"suggestions"`
}

// auditStore es lo que el caso de uso usa del registro de auditoría.
type auditStore interface {
	Create(ctx context.Context, e postgres.AuditEntry) (int64, error)
}

type Service struct {
	db postgres.DBTX     // lecturas sueltas (Get)
	tx postgres.TxRunner // Receive: todo en una transacción

	// audit es un campo para que el test pueda simular que falla al final y
	// ver que no queda nada de la compra.
	audit func(tx postgres.DBTX) auditStore
}

func New(db postgres.DBTX, tx postgres.TxRunner) *Service {
	return &Service{
		db:    db,
		tx:    tx,
		audit: func(tx postgres.DBTX) auditStore { return postgres.NewAuditLogStore(tx) },
	}
}

// batchKey identifica un lote dentro de la recepción: mismo producto, número,
// vencimiento y costo van al mismo lote.
type batchKey struct {
	productID int64
	number    string
	expiry    string
	unitCost  string // decimal.String(): "60" y "60.00" son el mismo costo
}

// Receive guarda una compra recibida. No modifica p: trabaja con una copia.
func (s *Service) Receive(ctx context.Context, p *model.Purchase) (*Result, error) {
	in := copyPurchase(p)
	if err := in.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}

	var res Result
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		if err := checkSupplier(ctx, tx, in.SupplierID); err != nil {
			return err
		}
		storeID, err := postgres.NewStoreStore(tx).ActiveID(ctx)
		if err != nil {
			// ErrNoActiveStore / ErrSeveralActiveStores cumplen store.ErrConflict;
			// se envuelven en service.ErrConflict para que la API responda 409.
			if errors.Is(err, store.ErrConflict) {
				return fmt.Errorf("%w: %w", service.ErrConflict, err)
			}
			return err
		}

		purchases := postgres.NewPurchaseStore(tx)
		saved, err := purchases.Create(ctx, in)
		if err != nil {
			return err
		}
		saved.Items = make([]*model.PurchaseItem, 0, len(in.Items))

		batches := map[batchKey]int64{}
		suggestions := map[int64]int{} // producto → posición en res.Suggestions
		for i, it := range in.Items {
			line, sug, err := s.receiveLine(ctx, tx, saved.ID, storeID, it, batches)
			if err != nil {
				return fmt.Errorf("line %d: %w", i+1, err)
			}
			saved.Items = append(saved.Items, line)
			if pos, ok := suggestions[sug.ProductID]; ok {
				res.Suggestions[pos] = sug
			} else {
				suggestions[sug.ProductID] = len(res.Suggestions)
				res.Suggestions = append(res.Suggestions, sug)
			}
		}

		_, err = s.audit(tx).Create(ctx, postgres.AuditEntry{
			Table:    "purchases",
			Action:   "INSERT",
			RecordID: saved.ID,
			NewData:  saved,
		})
		if err != nil {
			return err
		}
		res.Purchase = saved
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.TotalDifference = res.Purchase.TotalDifference()
	return &res, nil
}

// Get devuelve una compra con sus renglones.
//   - service.ErrInvalidID: id <= 0.
//   - service.ErrNotFound: no existe.
func (s *Service) Get(ctx context.Context, id int64) (*model.Purchase, error) {
	if id <= 0 {
		return nil, service.ErrInvalidID
	}
	p, err := postgres.NewPurchaseStore(s.db).GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	return p, nil
}

// receiveLine guarda un renglón con todo lo que mueve: lote, renglón,
// movimiento, saldo y costo.
func (s *Service) receiveLine(ctx context.Context, tx postgres.DBTX, purchaseID, storeID int64,
	it *model.PurchaseItem, batches map[batchKey]int64) (*model.PurchaseItem, Suggestion, error) {
	prod, err := checkProduct(ctx, tx, it)
	if err != nil {
		return nil, Suggestion{}, err
	}

	// Primero traba el stock y después lee el costo (igual que costs.Set):
	// otra recepción o carga manual del mismo producto espera a esta.
	onHand, err := postgres.NewStockBalanceStore(tx).LockOnHand(ctx, prod.ID)
	if err != nil {
		return nil, Suggestion{}, err
	}
	costStore := postgres.NewCostStore(tx)
	prev, err := costStore.Current(ctx, prod.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, Suggestion{}, err
	}

	// Lote: solo si el producto controla lotes.
	if prod.ControlsBatches {
		batchID, err := receiveBatch(ctx, tx, it, batches)
		if err != nil {
			return nil, Suggestion{}, err
		}
		it.BatchID = &batchID
	}

	it.PurchaseID = purchaseID
	line, err := postgres.NewPurchaseStore(tx).CreateItem(ctx, it)
	if err != nil {
		return nil, Suggestion{}, err
	}
	line.BatchNumber, line.ExpiryDate = it.BatchNumber, it.ExpiryDate

	_, err = postgres.NewStockMovementStore(tx).Create(ctx, &model.StockMovement{
		ProductID:    prod.ID,
		StoreID:      storeID,
		BatchID:      it.BatchID,
		MovementType: model.MovementPurchase,
		Quantity:     it.Quantity,
		UnitCost:     it.UnitCost,
		PurchaseID:   &purchaseID,
	})
	if err != nil {
		return nil, Suggestion{}, err
	}
	if _, err := postgres.NewStockBalanceStore(tx).Add(ctx, prod.ID, storeID, it.Quantity); err != nil {
		return nil, Suggestion{}, err
	}

	// Costo: promedio contra el stock de antes de este renglón (onHand).
	cost, err := costStore.Create(ctx, &model.Cost{
		ProductID: prod.ID,
		UnitCost:  it.UnitCost,
		AvgCost:   model.AverageCost(prev, onHand, it.UnitCost, it.Quantity),
	})
	if err != nil {
		return nil, Suggestion{}, err
	}
	suggested, err := costs.Suggest(ctx, tx, prod, it.UnitCost)
	if err != nil {
		return nil, Suggestion{}, err
	}

	return line, Suggestion{
		ProductID:      prod.ID,
		ProductName:    prod.Name,
		UnitCost:       cost.UnitCost,
		AvgCost:        cost.AvgCost,
		SuggestedPrice: suggested,
		CurrentPrice:   prod.CurrentPrice,
	}, nil
}

// receiveBatch crea el lote del renglón, o suma a uno igual de esta misma
// recepción, y devuelve su id.
func receiveBatch(ctx context.Context, tx postgres.DBTX, it *model.PurchaseItem, batches map[batchKey]int64) (int64, error) {
	key := batchKey{it.ProductID, it.BatchNumber, it.ExpiryDate, it.UnitCost.String()}
	batchStore := postgres.NewBatchStore(tx)
	if id, ok := batches[key]; ok {
		if _, err := batchStore.AddQuantity(ctx, id, it.Quantity); err != nil {
			return 0, err
		}
		return id, nil
	}
	b, err := batchStore.Create(ctx, &model.ProductBatch{
		ProductID:        it.ProductID,
		BatchNumber:      it.BatchNumber,
		ExpiryDate:       it.ExpiryDate,
		OriginalQuantity: it.Quantity,
		UnitCost:         it.UnitCost,
	})
	if err != nil {
		return 0, err
	}
	batches[key] = b.ID
	return b.ID, nil
}

func checkSupplier(ctx context.Context, tx postgres.DBTX, id int64) error {
	sup, err := postgres.NewSupplierStore(tx).GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrUnknownSupplier
		}
		return err
	}
	if !sup.IsActive {
		return ErrSupplierInactive
	}
	return nil
}

// checkProduct trae el producto del renglón y revisa que se pueda recibir y
// que el lote venga solo si el producto controla lotes.
func checkProduct(ctx context.Context, tx postgres.DBTX, it *model.PurchaseItem) (*model.Product, error) {
	prod, err := postgres.NewProductStore(tx).GetByID(ctx, it.ProductID)
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
	case prod.ControlsBatches && it.BatchNumber == "":
		return nil, ErrBatchRequired
	case !prod.ControlsBatches && (it.BatchNumber != "" || it.ExpiryDate != ""):
		return nil, ErrBatchNotAllowed
	}
	return prod, nil
}

// copyPurchase copia la compra y sus renglones: Validate normaliza (recorta
// espacios) y el caso de uso completa ids, y nada de eso tiene que tocar lo
// que mandó quien llama (si la transacción falla, el formulario vuelve con lo
// que se escribió).
func copyPurchase(p *model.Purchase) *model.Purchase {
	if p == nil {
		return nil
	}
	c := *p
	c.Items = make([]*model.PurchaseItem, len(p.Items))
	for i, it := range p.Items {
		if it != nil {
			cp := *it
			c.Items[i] = &cp
		}
	}
	return &c
}
