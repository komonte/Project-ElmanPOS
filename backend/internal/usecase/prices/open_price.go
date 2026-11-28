package prices

// Artículo sin registrar (migración 0022, 2026-11-28): un producto con precio
// libre ("Varios 21 %") que la caja usa para lo que todavía no está en el
// sistema. La cajera no da de alta productos en medio de una venta (saldrían
// duplicados, con el IVA o la unidad mal); escribe el precio y, si escaneó un
// código, queda guardado para darlo de alta después.
//   - Solo el admin lo marca (permiso prices, en la ruta): con precio libre
//     la caja cobra lo que se escriba.
//   - Solo productos por unidad y sin lotes: no tiene stock propio, la venta
//     no mueve nada.
//   - Queda en audit_log con quién.

import (
	"context"
	"errors"
	"fmt"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// ErrOpenPriceKind: un artículo sin registrar va por unidad y sin lotes.
var ErrOpenPriceKind = fmt.Errorf("an open-price item must be sold by unit and without batches: %w", service.ErrValidation)

// OpenPriceAllowed dice si el producto puede ser artículo sin registrar.
func OpenPriceAllowed(p *model.Product) bool {
	return p != nil && p.SaleType == model.SaleTypeUnit && !p.ControlsBatches
}

// SetOpenPrice marca (on) o desmarca el producto como artículo sin
// registrar; by es quién.
//   - service.ErrNotFound: el producto no existe.
//   - ErrProductInactive: está desactivado (solo al marcarlo).
//   - ErrOpenPriceKind: no va por unidad o controla lotes (solo al marcarlo).
func (s *Service) SetOpenPrice(ctx context.Context, by, productID int64, on bool) error {
	if productID <= 0 {
		return service.ErrInvalidID
	}
	return s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		products := postgres.NewProductStore(tx)
		prod, err := products.GetByID(ctx, productID)
		if errors.Is(err, store.ErrNotFound) {
			return service.ErrNotFound
		}
		if err != nil {
			return err
		}
		if on && !prod.IsActive {
			return ErrProductInactive
		}
		if on && !OpenPriceAllowed(prod) {
			return ErrOpenPriceKind
		}
		if err := products.SetOpenPrice(ctx, productID, on); err != nil {
			return err
		}
		_, err = s.audit(tx).Create(ctx, postgres.AuditEntry{
			UserID:   &by,
			Table:    "products",
			Action:   "UPDATE",
			RecordID: productID,
			NewData:  map[string]any{"open_price": on},
		})
		return err
	})
}
