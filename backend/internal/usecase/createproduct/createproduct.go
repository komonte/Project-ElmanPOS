// Package createproduct es el alta de un producto con su código de barras
// opcional (F-01 + F-02): los dos se guardan en una sola transacción, así no
// queda un producto creado a medias si el código falla (D-02, BR-04).
//
// Sin código es el alta de siempre. Editar un producto sigue pasando por el
// service genérico, y sus códigos por el caso de uso productcodes.
package createproduct

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/productcodes"
)

// codeStore es lo que el alta usa de los códigos dentro de la transacción.
type codeStore interface {
	productcodes.CodeFinder
	Add(ctx context.Context, c *model.ProductCode) (*model.ProductCode, error)
}

type Service struct {
	tx postgres.TxRunner

	// codes arma el store de códigos sobre la transacción. Es un campo para
	// que el test pueda simular que el INSERT del código falla después de
	// crear el producto (y ver que el producto se deshace).
	codes func(tx postgres.DBTX) codeStore
}

func New(tx postgres.TxRunner) *Service {
	return &Service{
		tx:    tx,
		codes: func(tx postgres.DBTX) codeStore { return postgres.NewProductCodeStore(tx) },
	}
}

// Create da de alta un producto y, si rawCode no está vacío, su código de
// barras. Los errores son los del service genérico (service.ErrValidation,
// ErrAlreadyExists, ErrConflict...); un código que ya es de otro producto da
// *productcodes.CodeTakenError.
//
// Orden: primero se valida todo lo que no necesita la DB (el producto, y
// después el código). Ya en la transacción, se revisa que el código esté
// libre ANTES de crear el producto: así un código repetido no gasta un
// número de sku, y la transacción no queda abortada para buscar el dueño.
func (s *Service) Create(ctx context.Context, p *model.Product, rawCode string) (*model.Product, error) {
	if p == nil {
		return nil, model.ErrNilProduct
	}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}

	var code *model.ProductCode
	if strings.TrimSpace(rawCode) != "" {
		code = &model.ProductCode{Code: rawCode}
		if err := code.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
		}
	}

	var created *model.Product
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		codes := s.codes(tx)
		if code != nil {
			if err := productcodes.CheckFree(ctx, codes, code.Code); err != nil {
				return err
			}
		}

		// El service genérico sobre la transacción: misma validación y
		// mismos errores que el alta sin código.
		products := service.NewService[model.Product](postgres.NewProductStore(tx),
			func(p *model.Product) error { return p.Validate() })
		var err error
		created, err = products.Create(ctx, p)
		if err != nil {
			return err
		}

		if code != nil {
			code.ProductID = created.ID
			if _, err := codes.Add(ctx, code); err != nil {
				if errors.Is(err, store.ErrDuplicateCode) {
					// Lo cargaron entre CheckFree y el INSERT: frenó el UNIQUE.
					return fmt.Errorf("%w: %w", service.ErrAlreadyExists, err)
				}
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return created, nil
}
