// Package prices es el caso de uso F-03: el precio de venta (PVP) de un
// producto. Un cambio de precio no edita el anterior: agrega uno nuevo que
// rige desde ya, y queda anotado en audit_log (NFR-03). Las ventas guardan su
// propio precio, así que un cambio nunca altera las ventas viejas (BR-03).
//
// Los errores salen como los del service genérico (service.ErrNotFound,
// service.ErrValidation...), así la API y la web los traducen igual.
package prices

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

// ErrProductInactive: no se le cambia el precio a un producto desactivado
// (F-03: el producto tiene que estar activo). Cumple service.ErrConflict, así
// la API responde 409.
var ErrProductInactive = fmt.Errorf("product is inactive: %w", service.ErrConflict)

// auditStore es lo que el caso de uso usa del registro de auditoría.
type auditStore interface {
	Create(ctx context.Context, e postgres.AuditEntry) (int64, error)
}

type Service struct {
	db postgres.DBTX     // lecturas sueltas (History)
	tx postgres.TxRunner // Set: precio y auditoría en una transacción

	// audit arma el store de auditoría sobre la transacción. Es un campo para
	// que el test pueda simular que falla después de guardar el precio (y
	// ver que el precio también se deshace).
	audit func(tx postgres.DBTX) auditStore
}

func New(db postgres.DBTX, tx postgres.TxRunner) *Service {
	return &Service{
		db:    db,
		tx:    tx,
		audit: func(tx postgres.DBTX) auditStore { return postgres.NewAuditLogStore(tx) },
	}
}

// Set le pone un precio nuevo a un producto, vigente desde ya.
//   - service.ErrValidation: el precio está mal (envuelve model.ErrInvalidPrice).
//   - service.ErrNotFound: el producto no existe.
//   - ErrProductInactive: el producto está desactivado.
//   - service.ErrConflict: ya tiene un precio con ese mismo "desde" (raro: dos
//     cambios en el mismo instante).
func (s *Service) Set(ctx context.Context, productID int64, unitPrice decimal.Decimal) (*model.Price, error) {
	if productID <= 0 {
		return nil, service.ErrInvalidID
	}
	p := &model.Price{ProductID: productID, UnitPrice: unitPrice}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}

	var saved *model.Price
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		if err := checkActive(ctx, postgres.NewProductStore(tx), productID); err != nil {
			return err
		}

		var err error
		saved, err = postgres.NewPriceStore(tx).Create(ctx, p)
		if err != nil {
			switch {
			case errors.Is(err, store.ErrDuplicate):
				return fmt.Errorf("%w: %w", service.ErrConflict, err)
			case errors.Is(err, store.ErrInvalidReference):
				return service.ErrNotFound // raro: el producto existía recién, en checkActive
			default:
				return err
			}
		}

		_, err = s.audit(tx).Create(ctx, postgres.AuditEntry{
			Table:    "prices",
			Action:   "INSERT",
			RecordID: saved.ID,
			NewData:  saved,
		})
		return err
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

// History devuelve los precios de un producto, del más nuevo al más viejo (el
// primero es el vigente). service.ErrNotFound si el producto no existe: una
// lista vacía diría "no tiene precio", que es otra cosa.
func (s *Service) History(ctx context.Context, productID int64) ([]*model.Price, error) {
	if productID <= 0 {
		return nil, service.ErrInvalidID
	}
	if _, err := postgres.NewProductStore(s.db).GetByID(ctx, productID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	return postgres.NewPriceStore(s.db).History(ctx, productID)
}

func checkActive(ctx context.Context, products *postgres.ProductStore, productID int64) error {
	prod, err := products.GetByID(ctx, productID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return service.ErrNotFound
		}
		return err
	}
	if !prod.IsActive {
		return ErrProductInactive
	}
	return nil
}
