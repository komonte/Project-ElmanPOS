// Package productcodes es el caso de uso F-02: los códigos de barra de un
// producto (listar, agregar, quitar). No pasa por la capa genérica (D-02):
// un código siempre se maneja desde su producto y su tipo lo calcula el model.
//
// Los errores salen como los del service genérico (service.ErrNotFound,
// service.ErrValidation...), así la API y la web los traducen igual.
package productcodes

import (
	"context"
	"errors"
	"fmt"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// ProductGetter es lo que el caso de uso necesita de los productos: saber si
// existe. Lo cumple *postgres.ProductStore.
type ProductGetter interface {
	GetByID(ctx context.Context, id int64) (*model.Product, error)
}

// CodeFinder busca de quién es un código. Lo cumple *postgres.ProductCodeStore.
type CodeFinder interface {
	FindByCode(ctx context.Context, code string) (*model.ProductCode, error)
}

// CodeStore es lo que el caso de uso usa de los códigos. Lo cumple
// *postgres.ProductCodeStore.
type CodeStore interface {
	CodeFinder
	ListByProduct(ctx context.Context, productID int64) ([]*model.ProductCode, error)
	Add(ctx context.Context, c *model.ProductCode) (*model.ProductCode, error)
	Delete(ctx context.Context, productID, codeID int64) error
}

type Service struct {
	products ProductGetter
	codes    CodeStore
}

func New(products ProductGetter, codes CodeStore) *Service {
	return &Service{products: products, codes: codes}
}

// List devuelve los códigos de un producto. service.ErrNotFound si el
// producto no existe (una lista vacía diría "no tiene", que es otra cosa).
func (s *Service) List(ctx context.Context, productID int64) ([]*model.ProductCode, error) {
	if productID <= 0 {
		return nil, service.ErrInvalidID
	}
	if _, err := s.products.GetByID(ctx, productID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	return s.codes.ListByProduct(ctx, productID)
}

// Add normaliza el código (como lo lee el escáner o lo escribe el usuario) y
// se lo agrega al producto. El tipo lo calcula el model.
//   - service.ErrValidation: el código está mal escrito (envuelve el error del model).
//   - *CodeTakenError: el código ya es de un producto (cumple service.ErrAlreadyExists).
//     Si dos lo cargan a la vez, el segundo recibe service.ErrAlreadyExists sin el nombre.
//   - service.ErrNotFound: el producto no existe.
func (s *Service) Add(ctx context.Context, productID int64, raw string) (*model.ProductCode, error) {
	if productID <= 0 {
		return nil, service.ErrInvalidID
	}
	c := &model.ProductCode{ProductID: productID, Code: raw}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}

	if err := CheckFree(ctx, s.codes, c.Code); err != nil {
		return nil, err
	}

	added, err := s.codes.Add(ctx, c)
	if err != nil {
		if errors.Is(err, store.ErrDuplicateCode) {
			// Lo cargaron entre CheckFree y el INSERT: frenó el UNIQUE.
			return nil, fmt.Errorf("%w: %w", service.ErrAlreadyExists, err)
		}
		if errors.Is(err, store.ErrInvalidReference) {
			// La única FK es el producto, que viene en la URL: es un 404.
			return nil, service.ErrNotFound
		}
		return nil, err
	}
	return added, nil
}

// Remove quita un código de un producto. service.ErrNotFound si no existe o
// es de otro producto.
func (s *Service) Remove(ctx context.Context, productID, codeID int64) error {
	if productID <= 0 || codeID <= 0 {
		return service.ErrInvalidID
	}
	if err := s.codes.Delete(ctx, productID, codeID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return service.ErrNotFound
		}
		return err
	}
	return nil
}

// CodeTakenError: el código ya es de un producto. Lleva cuál, para que el
// mensaje diga "ese código ya es de COCA COLA 2L" (o "de este producto").
// Cumple errors.Is con service.ErrAlreadyExists (la API responde 409) y con
// store.ErrDuplicateCode.
type CodeTakenError struct {
	Code        string
	ProductID   int64
	ProductSKU  string
	ProductName string
}

func (e *CodeTakenError) Error() string {
	return fmt.Sprintf("barcode %s already belongs to product %s %s", e.Code, e.ProductSKU, e.ProductName)
}

func (e *CodeTakenError) Unwrap() []error {
	return []error{service.ErrAlreadyExists, store.ErrDuplicateCode}
}

// CheckFree revisa que un código (ya normalizado) no sea de ningún producto.
// Si es de uno, devuelve un *CodeTakenError que dice de cuál.
//
// Se revisa ANTES del INSERT, no después de que falle: dentro de una
// transacción, un INSERT fallido la deja abortada y ya no se podría buscar
// el dueño. El UNIQUE de la DB sigue frenando el caso raro de dos altas al
// mismo tiempo (ahí el mensaje no dice de quién es).
func CheckFree(ctx context.Context, finder CodeFinder, code string) error {
	owner, err := finder.FindByCode(ctx, code)
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil
	case err != nil:
		return err
	}
	return &CodeTakenError{
		Code:        owner.Code,
		ProductID:   owner.ProductID,
		ProductSKU:  owner.ProductSKU,
		ProductName: owner.ProductName,
	}
}
