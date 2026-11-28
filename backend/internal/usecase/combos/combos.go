// Package combos es el caso de uso F-05: la receta de un combo (listar,
// poner y quitar componentes). Las reglas están en model.CheckComponent;
// vender el combo está en usecase/sales.
//
// Decisiones (2026-11-28): solo el admin cambia una receta (permiso combos,
// en la ruta): una receta mal cargada descuenta mal el stock en cada venta.
// Se guarda al instante, como los códigos de barra.
//
// Los errores salen como los del service genérico (service.ErrNotFound,
// service.ErrValidation...), así la API y la web los traducen igual.
package combos

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

// ErrComponentUnknown: el producto elegido como componente no existe.
var ErrComponentUnknown = fmt.Errorf("component product does not exist: %w", service.ErrValidation)

type Service struct {
	db postgres.DBTX
}

func New(db postgres.DBTX) *Service {
	return &Service{db: db}
}

// List devuelve los componentes del combo packID. service.ErrNotFound si el
// producto no existe.
func (s *Service) List(ctx context.Context, packID int64) ([]*model.ProductComponent, error) {
	if _, err := s.pack(ctx, packID); err != nil {
		return nil, err
	}
	return postgres.NewProductComponentStore(s.db).ListByPack(ctx, packID)
}

// Set pone componentID en el combo packID con la cantidad qty (por unidad
// del combo); si ya estaba, le cambia la cantidad.
//   - service.ErrNotFound: el combo no existe.
//   - service.ErrValidation: el combo no es combo, el componente no existe o
//     no sirve (otro combo, artículo sin registrar, inactivo), o la cantidad
//     está mal (envuelve el error del model).
func (s *Service) Set(ctx context.Context, packID, componentID int64, qty decimal.Decimal) (*model.ProductComponent, error) {
	c := &model.ProductComponent{PackID: packID, ComponentID: componentID, Quantity: qty}
	if err := c.Validate(); err != nil {
		if errors.Is(err, model.ErrComponentPack) {
			return nil, service.ErrInvalidID
		}
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}
	pack, err := s.pack(ctx, packID)
	if err != nil {
		return nil, err
	}
	comp, err := postgres.NewProductStore(s.db).GetByID(ctx, componentID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrComponentUnknown
	}
	if err != nil {
		return nil, err
	}
	if err := model.CheckComponent(pack, comp, qty); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}
	out, err := postgres.NewProductComponentStore(s.db).Set(ctx, c)
	if errors.Is(err, store.ErrInvalidReference) {
		return nil, ErrComponentUnknown // lo borraron recién
	}
	return out, err
}

// Remove quita el renglón id del combo packID. service.ErrNotFound si ese
// combo no lo tiene.
func (s *Service) Remove(ctx context.Context, packID, id int64) error {
	if packID <= 0 || id <= 0 {
		return service.ErrInvalidID
	}
	err := postgres.NewProductComponentStore(s.db).Delete(ctx, packID, id)
	if errors.Is(err, store.ErrNotFound) {
		return service.ErrNotFound
	}
	return err
}

// Candidates busca, por nombre, SKU o código de barras, los productos que
// pueden ir en el combo packID: activos, por unidad o por peso, que no sean
// artículos sin registrar ni el combo mismo.
func (s *Service) Candidates(ctx context.Context, packID int64, query string) ([]*model.Product, error) {
	found, err := postgres.NewProductStore(s.db).SearchByName(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]*model.Product, 0, len(found))
	for _, p := range found {
		if p.ID != packID && p.IsActive && p.SaleType != model.SaleTypeComposite && !p.OpenPrice {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *Service) pack(ctx context.Context, packID int64) (*model.Product, error) {
	if packID <= 0 {
		return nil, service.ErrInvalidID
	}
	p, err := postgres.NewProductStore(s.db).GetByID(ctx, packID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, service.ErrNotFound
	}
	return p, err
}
