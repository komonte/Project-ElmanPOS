// Package createproduct es el alta de un producto (F-01) con, opcionalmente:
//   - su código de barras (F-02);
//   - una marca nueva, si la que se busca no existe;
//   - una categoría nueva (nombre, padre e impuesto), si no está en la lista.
//
// Todo se guarda en una sola transacción (D-02, BR-04): si algo falla, no
// queda ni el producto ni la marca ni la categoría creados a medias. Así el
// alta no obliga a ir a otra pantalla, y no tienta a dejar el producto "sin
// marca" o mal categorizado.
//
// Editar un producto sigue pasando por el service genérico, y sus códigos
// por el caso de uso productcodes.
package createproduct

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/productcodes"
)

// Request es lo que se da de alta.
type Request struct {
	Product *model.Product

	// Barcode es el código de barras como se escribió o escaneó. Vacío: sin código.
	Barcode string

	// NewBrand es el nombre de una marca que no se encontró en el buscador.
	// Si no está vacío, se ignora Product.BrandID.
	NewBrand string

	// NewCategory es una categoría que no estaba en la lista (Name, ParentID
	// y TaxID). Si no es nil, se ignora Product.CategoryID.
	NewCategory *model.Category
}

// BrandInactiveError: se pidió crear una marca que ya existe, pero está
// desactivada. No se reactiva sola (alguien la desactivó por algo): se avisa.
// Cumple errors.Is con service.ErrConflict (la API responde 409).
type BrandInactiveError struct {
	Name string
}

func (e *BrandInactiveError) Error() string {
	return fmt.Sprintf("brand %s already exists but is inactive", e.Name)
}

func (e *BrandInactiveError) Unwrap() error { return service.ErrConflict }

// codeStore es lo que el alta usa de los códigos dentro de la transacción.
type codeStore interface {
	productcodes.CodeFinder
	Add(ctx context.Context, c *model.ProductCode) (*model.ProductCode, error)
}

type Service struct {
	tx postgres.TxRunner

	// codes arma el store de códigos sobre la transacción. Es un campo para
	// que el test pueda simular que el INSERT del código falla después de
	// crear el producto (y ver que todo se deshace).
	codes func(tx postgres.DBTX) codeStore
}

func New(tx postgres.TxRunner) *Service {
	return &Service{
		tx:    tx,
		codes: func(tx postgres.DBTX) codeStore { return postgres.NewProductCodeStore(tx) },
	}
}

// pendingCategoryID ocupa el lugar de la categoría nueva al validar el
// producto: todavía no tiene id (lo da la DB al crearla, ya en la
// transacción), y Validate exige uno.
const pendingCategoryID int64 = math.MaxInt64

// Create da de alta el producto y lo que pida req. Los errores son los del
// service genérico (service.ErrValidation, ErrAlreadyExists, ErrConflict...),
// más *productcodes.CodeTakenError (el código ya es de otro producto) y
// *BrandInactiveError.
//
// Orden: primero se valida todo lo que no necesita la DB (producto, marca,
// categoría, código), así un error de tipeo no abre una transacción. Ya en
// ella, se revisa lo que se podría informar (de quién es el código, si la
// marca existe) ANTES de escribir: un INSERT fallido deja la transacción
// abortada y ya no se podría consultar nada.
func (s *Service) Create(ctx context.Context, req Request) (*model.Product, error) {
	if req.Product == nil {
		return nil, model.ErrNilProduct
	}
	// Se trabaja sobre una copia: si la transacción falla, req.Product no
	// queda apuntando a una marca o categoría que se deshizo, y el formulario
	// vuelve con lo que escribió el usuario.
	p := *req.Product

	var brand *model.Brand
	if strings.TrimSpace(req.NewBrand) != "" {
		p.BrandID = nil
		brand = &model.Brand{Name: req.NewBrand}
	}
	var newCategory *model.Category
	if req.NewCategory != nil {
		c := *req.NewCategory
		newCategory = &c
		p.CategoryID = pendingCategoryID
	}

	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}
	if brand != nil {
		if err := brand.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
		}
	}
	if newCategory != nil {
		if err := newCategory.Validate(); err != nil {
			return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
		}
	}
	var code *model.ProductCode
	if strings.TrimSpace(req.Barcode) != "" {
		code = &model.ProductCode{Code: req.Barcode}
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

		if brand != nil {
			id, err := s.brandID(ctx, tx, brand)
			if err != nil {
				return err
			}
			p.BrandID = &id
		}

		if newCategory != nil {
			// El service genérico sobre la transacción: mismas reglas que la
			// pantalla de categorías (padre inactivo, nombre repetido...).
			categories := service.NewService[model.Category](postgres.NewCategoryStore(tx),
				func(c *model.Category) error { return c.Validate() })
			c, err := categories.Create(ctx, newCategory)
			if err != nil {
				return err
			}
			p.CategoryID = c.ID
		}

		products := service.NewService[model.Product](postgres.NewProductStore(tx),
			func(p *model.Product) error { return p.Validate() })
		var err error
		created, err = products.Create(ctx, &p)
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

// brandID devuelve el id de la marca a usar: la que ya existe con ese nombre
// exacto (si está activa), o una nueva. Que exista puede pasar: el buscador
// ofrece crear solo si no la encuentra, pero otro la pudo crear mientras tanto.
func (s *Service) brandID(ctx context.Context, tx postgres.DBTX, brand *model.Brand) (int64, error) {
	brands := postgres.NewBrandStore(tx)
	existing, err := brands.GetByName(ctx, brand.Name)
	switch {
	case err == nil && existing.IsActive:
		return existing.ID, nil
	case err == nil:
		return 0, &BrandInactiveError{Name: existing.Name}
	case !errors.Is(err, store.ErrNotFound):
		return 0, err
	}

	created, err := service.NewService[model.Brand](brands,
		func(b *model.Brand) error { return b.Validate() }).Create(ctx, brand)
	if err != nil {
		return 0, err
	}
	return created.ID, nil
}
