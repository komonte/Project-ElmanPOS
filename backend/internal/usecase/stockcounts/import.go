package stockcounts

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/countcsv"
	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Importar un conteo desde una planilla (countcsv): cada fila es una carga en
// el conteo en borrador, como si se hubiera escaneado, con las mismas reglas.
// Después se revisa y confirma igual que siempre: la planilla no mueve stock.
//
// Decisiones (2026-11-26):
//   - El código se busca primero como código de barras y después como SKU.
//     Nunca por nombre (hay demasiados parecidos).
//   - Las filas con problemas quedan afuera y se listan; las demás se cargan.
//   - El mismo producto en varias filas se suma (como en distintos sectores).
//   - Sin sector en la fila, el sector por defecto de la importación.
//   - Costo y vencimiento van al renglón del producto (el último que aparece
//     gana) y se pueden cambiar en la revisión.

var (
	ErrImportSectorRequired = fmt.Errorf("the default sector is required: %w", service.ErrValidation)
	ErrCodeNotFound         = errors.New("no product has that barcode or SKU")
)

// ImportLine es una fila que entra: el producto encontrado y lo que se carga.
type ImportLine struct {
	countcsv.Row
	ProductID   int64
	ProductName string
}

// ImportProblem es una fila que no entra y por qué (el error del lector, del
// modelo o de la búsqueda del producto).
type ImportProblem struct {
	Line int
	Code string
	Err  error
}

// ImportResult es lo que devuelve Import: las filas que entran, las que no y
// si se cargaron (Apply) o es la vista previa.
type ImportResult struct {
	Lines    []ImportLine
	Problems []ImportProblem
	Applied  bool
}

// Import revisa las filas de una planilla contra el catálogo y, con apply,
// las carga en el conteo en borrador, todo en una transacción. Sin apply es
// la vista previa: no guarda nada. problems son los problemas que ya dio el
// lector de la planilla (filas ilegibles): se suman a la lista.
//   - service.ErrValidation: falta el sector por defecto.
//   - service.ErrNotFound: el conteo no existe.
//   - service.ErrConflict: el conteo ya está confirmado.
func (s *Service) Import(ctx context.Context, countID int64, rows []countcsv.Row, problems []countcsv.LineError,
	defaultSector string, apply bool) (*ImportResult, error) {
	if countID <= 0 {
		return nil, service.ErrInvalidID
	}
	defaultSector = strings.Join(strings.Fields(defaultSector), " ")
	if defaultSector == "" {
		return nil, ErrImportSectorRequired
	}

	res := &ImportResult{}
	for _, p := range problems {
		res.Problems = append(res.Problems, ImportProblem{Line: p.Line, Err: p.Err})
	}
	run := func(tx postgres.DBTX) error {
		counts := postgres.NewStockCountStore(tx)
		c, err := counts.LockDraft(ctx, countID)
		if err != nil {
			return err
		}
		res.Lines, res.Problems = checkRows(ctx, tx, rows, defaultSector, res.Problems)
		if !apply {
			return nil
		}
		for _, l := range res.Lines {
			entry := &model.StockCountEntry{ProductID: l.ProductID, Sector: l.Sector, Quantity: l.Quantity}
			it, err := addEntry(ctx, tx, c, entry)
			if err != nil {
				return fmt.Errorf("line %d: %w", l.Line, err)
			}
			if l.Cost == nil && l.Expiry == "" {
				continue
			}
			cost, expiry := it.UnitCost, it.ExpiryDate // lo que la fila no trae, queda
			if l.Cost != nil {
				cost = l.Cost
			}
			if l.Expiry != "" {
				expiry = l.Expiry
			}
			if err := counts.SetItemDetails(ctx, c.ID, it.ID, cost, expiry); err != nil {
				return fmt.Errorf("line %d: %w", l.Line, err)
			}
		}
		res.Applied = true
		return nil
	}

	// También la vista previa corre en una transacción (que no escribe nada):
	// LockDraft dice si el conteo existe y sigue en borrador.
	if err := s.tx.InTx(ctx, run); err != nil {
		return nil, translate(err)
	}
	return res, nil
}

// checkRows busca el producto de cada fila y la revisa con las reglas de una
// carga a mano. Devuelve las que entran y suma a problems las que no, en el
// orden de la planilla.
func checkRows(ctx context.Context, tx postgres.DBTX, rows []countcsv.Row, defaultSector string,
	problems []ImportProblem) ([]ImportLine, []ImportProblem) {
	var lines []ImportLine
	for _, row := range rows {
		if row.Sector == "" {
			row.Sector = defaultSector
		}
		line, err := checkRow(ctx, tx, row)
		if err != nil {
			problems = append(problems, ImportProblem{Line: row.Line, Code: row.Code, Err: err})
			continue
		}
		lines = append(lines, line)
	}
	// Los del lector y los de la búsqueda, en el orden de la planilla.
	slices.SortStableFunc(problems, func(a, b ImportProblem) int { return cmp.Compare(a.Line, b.Line) })
	return lines, problems
}

func checkRow(ctx context.Context, tx postgres.DBTX, row countcsv.Row) (ImportLine, error) {
	prod, err := productByCode(ctx, tx, row.Code)
	if err != nil {
		return ImportLine{}, err
	}
	switch {
	case !prod.IsActive:
		return ImportLine{}, ErrProductInactive
	case prod.SaleType == model.SaleTypeComposite:
		return ImportLine{}, ErrCompositeProduct
	}
	if err := prod.CheckWholeQuantity(row.Quantity); err != nil {
		return ImportLine{}, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}
	entry := &model.StockCountEntry{ProductID: prod.ID, Sector: row.Sector, Quantity: row.Quantity}
	if err := entry.Validate(); err != nil {
		return ImportLine{}, err
	}
	row.Sector = entry.Sector
	if err := (&model.StockCountItem{UnitCost: row.Cost, ExpiryDate: row.Expiry}).Validate(); err != nil {
		return ImportLine{}, err
	}
	if row.Expiry != "" && !prod.ControlsBatches {
		return ImportLine{}, ErrExpiryNotAllowed
	}
	return ImportLine{Row: row, ProductID: prod.ID, ProductName: prod.Name}, nil
}

// productByCode busca el producto de un código de la planilla: primero como
// código de barras (normalizado como se guarda), después como SKU.
func productByCode(ctx context.Context, tx postgres.DBTX, code string) (*model.Product, error) {
	products := postgres.NewProductStore(tx)
	if barcode, err := model.NormalizeBarcode(code); err == nil {
		pc, err := postgres.NewProductCodeStore(tx).FindByCode(ctx, barcode)
		switch {
		case err == nil:
			return products.GetByID(ctx, pc.ProductID)
		case !errors.Is(err, store.ErrNotFound):
			return nil, err
		}
	}
	p, err := products.GetBySKU(ctx, code)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrCodeNotFound
	}
	return p, err
}
