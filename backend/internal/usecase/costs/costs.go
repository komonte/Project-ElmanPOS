// Package costs es el caso de uso F-04 con carga manual: el costo de un
// producto. Igual que el precio, un costo nuevo no edita el anterior: agrega
// uno que rige desde ya, con su costo promedio recalculado, y queda anotado en
// audit_log (NFR-03).
//
// Con el costo nuevo se sugiere un precio de venta según el margen del
// producto y el IVA de su categoría (el costo va sin IVA y el precio lo
// incluye). La sugerencia no se guarda: el usuario la acepta como precio
// nuevo (F-03, usecase/prices) o la ignora.
//
// La recepción de compras (F-06) va a guardar costos dentro de su propia
// transacción, con la cantidad que entra; usa las mismas cuentas
// (model.AverageCost).
//
// Los errores salen como los del service genérico (service.ErrNotFound,
// service.ErrValidation...), así la API y la web los traducen igual.
package costs

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

// ErrProductInactive: no se le carga costo a un producto desactivado. Cumple
// service.ErrConflict, así la API responde 409.
var ErrProductInactive = fmt.Errorf("product is inactive: %w", service.ErrConflict)

// Result es lo que devuelve Set: el costo guardado y el precio sugerido.
// SuggestedPrice es nil si el producto tiene margen 0 (se pone a mano).
type Result struct {
	Cost           *model.Cost      `json:"cost"`
	SuggestedPrice *decimal.Decimal `json:"suggested_price"`
}

// auditStore es lo que el caso de uso usa del registro de auditoría.
type auditStore interface {
	Create(ctx context.Context, e postgres.AuditEntry) (int64, error)
}

type Service struct {
	db postgres.DBTX     // lecturas sueltas (History)
	tx postgres.TxRunner // Set: costo y auditoría en una transacción

	// audit arma el store de auditoría sobre la transacción. Es un campo para
	// que el test pueda simular que falla después de guardar el costo.
	audit func(tx postgres.DBTX) auditStore
}

func New(db postgres.DBTX, tx postgres.TxRunner) *Service {
	return &Service{
		db:    db,
		tx:    tx,
		audit: func(tx postgres.DBTX) auditStore { return postgres.NewAuditLogStore(tx) },
	}
}

// Set carga a mano un costo nuevo, vigente desde ya. El costo promedio se
// calcula con model.AverageCost sin mercadería que entre: si el producto no
// tenía costo o no tiene stock, el promedio es el costo nuevo; si no, queda
// igual.
//   - service.ErrValidation: el costo está mal (envuelve model.ErrInvalidCost).
//   - service.ErrNotFound: el producto no existe.
//   - ErrProductInactive: el producto está desactivado.
//   - service.ErrConflict: ya tiene un costo con ese mismo "desde" (raro).
func (s *Service) Set(ctx context.Context, productID int64, unitCost decimal.Decimal) (*Result, error) {
	if productID <= 0 {
		return nil, service.ErrInvalidID
	}
	c := &model.Cost{ProductID: productID, UnitCost: unitCost}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}

	var res Result
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		prod, err := activeProduct(ctx, postgres.NewProductStore(tx), productID)
		if err != nil {
			return err
		}

		// Primero traba el stock y después lee el costo: si entra una compra
		// del mismo producto al mismo tiempo, espera acá a que termine y lee
		// el costo que dejó. Leyendo el costo antes de trabar, después de
		// esperar seguiría teniendo el viejo y lo pisaría.
		onHand, err := postgres.NewStockBalanceStore(tx).LockOnHand(ctx, productID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return service.ErrNotFound // raro: el producto existía recién
			}
			return err
		}
		costs := postgres.NewCostStore(tx)
		prev, err := costs.Current(ctx, productID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		// Sin costo previo, prev queda en nil (lo que espera AverageCost).
		c.AvgCost = model.AverageCost(prev, onHand, unitCost, decimal.Zero)

		res.Cost, err = costs.Create(ctx, c)
		if err != nil {
			switch {
			case errors.Is(err, store.ErrDuplicate):
				return fmt.Errorf("%w: %w", service.ErrConflict, err)
			case errors.Is(err, store.ErrInvalidReference):
				return service.ErrNotFound // raro: el producto existía recién
			default:
				return err
			}
		}

		res.SuggestedPrice, err = suggestedPrice(ctx, tx, prod, unitCost)
		if err != nil {
			return err
		}

		_, err = s.audit(tx).Create(ctx, postgres.AuditEntry{
			Table:    "product_costs",
			Action:   "INSERT",
			RecordID: res.Cost.ID,
			NewData:  res.Cost,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// SuggestedPrice devuelve el precio sugerido para un producto y un costo sin
// IVA: con el margen del producto y el IVA de su categoría
// (model.SuggestPrice). nil si no hay sugerencia (margen 0). La pantalla lo
// usa para el costo vigente; Set, para el costo que guarda.
func (s *Service) SuggestedPrice(ctx context.Context, prod *model.Product, unitCost decimal.Decimal) (*decimal.Decimal, error) {
	return suggestedPrice(ctx, s.db, prod, unitCost)
}

// suggestedPrice busca el IVA del producto (categoría → impuesto) y calcula la
// sugerencia. Con margen 0 no consulta nada: no hay sugerencia igual.
func suggestedPrice(ctx context.Context, db postgres.DBTX, prod *model.Product, unitCost decimal.Decimal) (*decimal.Decimal, error) {
	if !prod.MarginPercent.IsPositive() {
		return nil, nil
	}
	cat, err := postgres.NewCategoryStore(db).GetByID(ctx, prod.CategoryID)
	if err != nil {
		return nil, err
	}
	tax, err := postgres.NewTaxStore(db).GetByID(ctx, cat.TaxID)
	if err != nil {
		return nil, err
	}
	price, ok := model.SuggestPrice(unitCost, prod.MarginPercent, tax.Rate)
	if !ok {
		return nil, nil
	}
	return &price, nil
}

// History devuelve los costos de un producto, del más nuevo al más viejo (el
// primero es el vigente). service.ErrNotFound si el producto no existe: una
// lista vacía diría "no tiene costo", que es otra cosa.
func (s *Service) History(ctx context.Context, productID int64) ([]*model.Cost, error) {
	if productID <= 0 {
		return nil, service.ErrInvalidID
	}
	if _, err := postgres.NewProductStore(s.db).GetByID(ctx, productID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	return postgres.NewCostStore(s.db).History(ctx, productID)
}

// activeProduct trae el producto (hace falta su margen) y revisa que exista y
// esté activo.
func activeProduct(ctx context.Context, products *postgres.ProductStore, productID int64) (*model.Product, error) {
	prod, err := products.GetByID(ctx, productID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	if !prod.IsActive {
		return nil, ErrProductInactive
	}
	return prod, nil
}
