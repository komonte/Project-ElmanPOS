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

// Set le pone un precio nuevo a un producto, vigente desde ya. by es quién:
// queda en el user_id de la auditoría (F-13).
//   - service.ErrValidation: el precio está mal (envuelve model.ErrInvalidPrice).
//   - service.ErrNotFound: el producto no existe.
//   - ErrProductInactive: el producto está desactivado.
//   - service.ErrConflict: ya tiene un precio con ese mismo "desde" (raro: dos
//     cambios en el mismo instante).
func (s *Service) Set(ctx context.Context, by, productID int64, unitPrice decimal.Decimal) (*model.Price, error) {
	saved, err := s.SetMany(ctx, by, []Change{{ProductID: productID, UnitPrice: unitPrice}})
	if err != nil {
		var ce *ChangeError
		if errors.As(err, &ce) {
			return nil, ce.Err // con uno solo, el error del producto tal cual
		}
		return nil, err
	}
	return saved[0], nil
}

// Change es un precio nuevo para un producto.
type Change struct {
	ProductID int64
	UnitPrice decimal.Decimal
}

// ChangeError dice qué precio de SetMany falló (Index, desde 0) y por qué.
// errors.Is/As llegan al error de adentro (service.ErrValidation...).
type ChangeError struct {
	Index     int
	ProductID int64
	Err       error
}

func (e *ChangeError) Error() string {
	return fmt.Sprintf("price %d (product %d): %v", e.Index+1, e.ProductID, e.Err)
}

func (e *ChangeError) Unwrap() error { return e.Err }

// SetMany guarda varios precios nuevos en una sola transacción: todos o
// ninguno ("Guardar todos" de una factura). Cada uno queda en la auditoría
// con by. Un error dice cuál falló (ChangeError) con los mismos errores que
// Set.
func (s *Service) SetMany(ctx context.Context, by int64, changes []Change) ([]*model.Price, error) {
	for i, c := range changes {
		if c.ProductID <= 0 {
			return nil, &ChangeError{Index: i, ProductID: c.ProductID, Err: service.ErrInvalidID}
		}
		p := &model.Price{ProductID: c.ProductID, UnitPrice: c.UnitPrice}
		if err := p.Validate(); err != nil {
			return nil, &ChangeError{Index: i, ProductID: c.ProductID, Err: fmt.Errorf("%w: %w", service.ErrValidation, err)}
		}
	}

	out := make([]*model.Price, 0, len(changes))
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		for i, c := range changes {
			saved, err := s.set(ctx, tx, by, c)
			if err != nil {
				return &ChangeError{Index: i, ProductID: c.ProductID, Err: err}
			}
			out = append(out, saved)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// set guarda un precio (ya validado) y su auditoría, dentro de tx.
func (s *Service) set(ctx context.Context, tx postgres.DBTX, by int64, c Change) (*model.Price, error) {
	if err := checkActive(ctx, postgres.NewProductStore(tx), c.ProductID); err != nil {
		return nil, err
	}
	saved, err := postgres.NewPriceStore(tx).Create(ctx, &model.Price{ProductID: c.ProductID, UnitPrice: c.UnitPrice})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrDuplicate):
			return nil, fmt.Errorf("%w: %w", service.ErrConflict, err)
		case errors.Is(err, store.ErrInvalidReference):
			return nil, service.ErrNotFound // raro: el producto existía recién, en checkActive
		default:
			return nil, err
		}
	}
	_, err = s.audit(tx).Create(ctx, postgres.AuditEntry{
		UserID:   &by,
		Table:    "prices",
		Action:   "INSERT",
		RecordID: saved.ID,
		NewData:  saved,
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
