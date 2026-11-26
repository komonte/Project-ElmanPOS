// Package sales es el caso de uso F-07 (venta atómica) con F-10 (venta por
// peso): cobra el carrito de la caja y descuenta el stock, todo o nada
// (BR-04).
//
// Decisiones (2026-11-26):
//   - Se vende en la sesión abierta de la única caja del local activo. Sin
//     caja abierta no se vende (BR-02).
//   - Solo productos activos, que no sean combos y que tengan precio.
//   - Por unidad, cantidades enteras; por peso, kg con hasta 3 decimales y
//     no más que app_settings max_sale_weight_kg.
//   - Sin stock suficiente en el local, la venta se rechaza entera: el stock
//     inicial se carga antes con un conteo.
//   - Precio vigente y IVA de la categoría, copiados en el renglón (BR-03).
//   - De un producto con lotes, sale de los lotes en orden FIFO; lo que los
//     lotes no cubren sale sin lote. Cada lote a su costo y sin lote al costo
//     promedio vigente. El promedio no cambia.
//   - Sin descuentos. Los pagos tienen que cubrir el total; solo el efectivo
//     puede pasarse (el vuelto no se guarda).
//   - Número de venta por caja, sin repetirse entre sesiones.
//   - Combos (expandir sus componentes) quedan para después.
//   - Sin login todavía: performed_by queda NULL.
//
// Los errores salen como los del service genérico (service.ErrValidation,
// service.ErrConflict...), así la API y la web los traducen igual.
package sales

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// MaxWeightKey es el ajuste con el peso máximo por renglón (migración 0017).
const MaxWeightKey = "max_sale_weight_kg"

// defaultMaxWeight se usa si el ajuste no está (alguien lo borró).
var defaultMaxWeight = decimal.NewFromInt(50)

var (
	ErrRegisterClosed   = fmt.Errorf("the cash register is not open: %w", service.ErrConflict)
	ErrUnknownProduct   = fmt.Errorf("product does not exist: %w", service.ErrValidation)
	ErrProductInactive  = fmt.Errorf("product is inactive: %w", service.ErrConflict)
	ErrCompositeProduct = fmt.Errorf("composite products cannot be sold yet: %w", service.ErrValidation)
	ErrNoPrice          = fmt.Errorf("product has no price: %w", service.ErrConflict)
	ErrNotEnoughStock   = fmt.Errorf("not enough stock: %w", service.ErrConflict)
)

// LineError es un problema con un renglón: dice qué producto es, para el
// mensaje, y envuelve el error puntual (ErrNoPrice, ErrNotEnoughStock, un
// error de cantidad del model...). Con ErrNotEnoughStock, Available es lo
// que hay en el local.
type LineError struct {
	ProductID   int64
	ProductName string
	Available   decimal.Decimal
	Err         error
}

func (e *LineError) Error() string {
	if errors.Is(e.Err, ErrNotEnoughStock) {
		return fmt.Sprintf("%s: %v (there is %s)", e.ProductName, e.Err, e.Available)
	}
	return fmt.Sprintf("%s: %v", e.ProductName, e.Err)
}

func (e *LineError) Unwrap() error { return e.Err }

type Service struct {
	db postgres.DBTX     // lecturas sueltas
	tx postgres.TxRunner // Checkout: todo en una transacción
}

func New(db postgres.DBTX, tx postgres.TxRunner) *Service {
	return &Service{db: db, tx: tx}
}

// Checkout cobra una venta. De in usa los productos y cantidades de los
// renglones y los pagos; precios, IVA, costos y totales los pone acá. No
// modifica in.
//
// En una transacción: traba la sesión, traba el stock de cada producto (en
// orden de id, para que dos cajas no se bloqueen entre sí), revisa cada
// renglón, calcula totales y pagos, guarda la venta con sus lotes y pagos,
// los movimientos SALE y resta el saldo. Devuelve la venta guardada, con el
// nombre de cada producto, el de la caja y el vuelto (Change), para el
// ticket.
//   - service.ErrValidation: la venta está mal (envuelve el error del model:
//     cantidades, pagos que no alcanzan, solo efectivo puede pasarse...).
//   - service.ErrConflict: la caja no está abierta (ErrRegisterClosed) o no
//     hay un único local o caja.
//   - *LineError: un renglón no se puede vender (sin precio, sin stock,
//     inactivo, combo, cantidad mal); cumple ErrValidation o ErrConflict
//     según el error que envuelve.
func (s *Service) Checkout(ctx context.Context, in *model.Sale) (*model.Sale, error) {
	if in == nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, model.ErrNilSale)
	}
	sale := copyInput(in)
	if err := sale.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}

	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		storeID, sess, err := lockSession(ctx, tx)
		if err != nil {
			return err
		}
		sale.CashierSessionID, sale.RegisterName = sess.ID, sess.RegisterName

		maxWeight, err := postgres.NewSettingStore(tx).Decimal(ctx, MaxWeightKey)
		if errors.Is(err, store.ErrNotFound) {
			maxWeight = defaultMaxWeight
		} else if err != nil {
			return err
		}

		// Primero traba todo el stock, en orden de id, y después lee.
		balances := postgres.NewStockBalanceStore(tx)
		ids := make([]int64, len(sale.Items))
		for i, it := range sale.Items {
			ids[i] = it.ProductID
		}
		slices.Sort(ids)
		for _, id := range ids {
			if _, err := balances.LockOnHand(ctx, id); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return &LineError{ProductID: id, Err: ErrUnknownProduct}
				}
				return err
			}
		}

		products := make(map[int64]*model.Product, len(sale.Items))
		for _, it := range sale.Items {
			prod, err := priceLine(ctx, tx, it, storeID, maxWeight)
			if err != nil {
				return err
			}
			products[it.ProductID] = prod
		}

		if err := sale.ComputeTotals(); err != nil {
			return lineOrValidation(sale, err)
		}
		if err := sale.SettlePayments(); err != nil {
			return fmt.Errorf("%w: %w", service.ErrValidation, err)
		}

		sale.SaleNumber, err = postgres.NewSaleStore(tx).NextNumber(ctx, sess.CashRegisterID)
		if err != nil {
			return err
		}
		// Los lotes salen antes de guardar: la venta guarda de qué lote salió
		// cada renglón y su costo.
		unbatched := make(map[int64]decimal.Decimal, len(sale.Items))
		avgCosts := make(map[int64]decimal.Decimal, len(sale.Items))
		for _, it := range sale.Items {
			left, avg, err := takeBatches(ctx, tx, products[it.ProductID], it)
			if err != nil {
				return err
			}
			unbatched[it.ProductID], avgCosts[it.ProductID] = left, avg
		}
		if err := postgres.NewSaleStore(tx).Create(ctx, sale); err != nil {
			return err
		}

		movements := postgres.NewStockMovementStore(tx)
		move := func(it *model.SaleItem, batch *int64, qty, unitCost decimal.Decimal) error {
			_, err := movements.Create(ctx, &model.StockMovement{
				ProductID:    it.ProductID,
				StoreID:      storeID,
				BatchID:      batch,
				MovementType: model.MovementSale,
				Quantity:     qty.Neg(),
				UnitCost:     unitCost,
				SaleID:       &sale.ID,
			})
			return err
		}
		for _, it := range sale.Items {
			for _, b := range it.Batches {
				if err := move(it, &b.BatchID, b.Quantity, b.UnitCost); err != nil {
					return err
				}
			}
			if left := unbatched[it.ProductID]; left.IsPositive() {
				if err := move(it, nil, left, avgCosts[it.ProductID]); err != nil {
					return err
				}
			}
			if _, err := balances.Add(ctx, it.ProductID, storeID, it.Quantity.Neg()); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, translate(err)
	}
	return sale, nil
}

// copyInput copia de in solo lo que manda la caja: así nada que venga de
// afuera (un precio, un costo, otra sesión) llega a la DB.
func copyInput(in *model.Sale) *model.Sale {
	out := &model.Sale{}
	for _, it := range in.Items {
		if it == nil {
			out.Items = append(out.Items, nil)
			continue
		}
		out.Items = append(out.Items, &model.SaleItem{ProductID: it.ProductID, Quantity: it.Quantity})
	}
	for _, p := range in.Payments {
		if p == nil {
			out.Payments = append(out.Payments, nil)
			continue
		}
		out.Payments = append(out.Payments, &model.Payment{Method: p.Method, Amount: p.Amount, Reference: p.Reference})
	}
	return out
}

// lockSession busca la sesión abierta de la única caja del local activo y la
// traba hasta el final de la transacción.
func lockSession(ctx context.Context, tx postgres.DBTX) (int64, *model.CashierSession, error) {
	storeID, err := postgres.NewStoreStore(tx).ActiveID(ctx)
	if err != nil {
		return 0, nil, err
	}
	reg, err := postgres.NewCashRegisterStore(tx).Only(ctx, storeID)
	if errors.Is(err, store.ErrNotFound) {
		return 0, nil, ErrRegisterClosed
	}
	if err != nil {
		return 0, nil, err
	}
	sessions := postgres.NewCashierSessionStore(tx)
	open, err := sessions.OpenFor(ctx, reg.ID)
	if errors.Is(err, store.ErrNotFound) {
		return 0, nil, ErrRegisterClosed
	}
	if err != nil {
		return 0, nil, err
	}
	// Entre leerla y trabarla se pudo cerrar: LockOpen lo vuelve a revisar.
	sess, err := sessions.LockOpen(ctx, open.ID)
	if errors.Is(err, store.ErrSessionNotOpen) {
		return 0, nil, ErrRegisterClosed
	}
	return storeID, sess, err
}

// priceLine revisa un renglón (producto vendible, cantidad, stock) y le pone
// precio, IVA y nombre. El stock ya está trabado.
func priceLine(ctx context.Context, tx postgres.DBTX, it *model.SaleItem, storeID int64,
	maxWeight decimal.Decimal) (*model.Product, error) {
	prod, err := postgres.NewProductStore(tx).GetByID(ctx, it.ProductID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, &LineError{ProductID: it.ProductID, Err: ErrUnknownProduct}
	}
	if err != nil {
		return nil, err
	}
	lineErr := func(err error) *LineError {
		return &LineError{ProductID: prod.ID, ProductName: prod.Name, Err: err}
	}
	switch {
	case !prod.IsActive:
		return nil, lineErr(ErrProductInactive)
	case prod.SaleType == model.SaleTypeComposite:
		return nil, lineErr(ErrCompositeProduct)
	}
	if err := it.ValidateQuantity(prod.SaleType, maxWeight); err != nil {
		return nil, lineErr(fmt.Errorf("%w: %w", service.ErrValidation, err))
	}

	price, err := postgres.NewPriceStore(tx).Current(ctx, prod.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, lineErr(ErrNoPrice)
	}
	if err != nil {
		return nil, err
	}
	rate, err := taxRate(ctx, tx, prod)
	if err != nil {
		return nil, err
	}

	available, err := postgres.NewStockBalanceStore(tx).OnHandAt(ctx, prod.ID, storeID)
	if err != nil {
		return nil, err
	}
	if it.Quantity.GreaterThan(available) {
		e := lineErr(ErrNotEnoughStock)
		e.Available = available
		return nil, e
	}

	it.UnitPrice, it.TaxRate = price.UnitPrice, rate
	it.ProductName, it.SaleType = prod.Name, prod.SaleType
	return prod, nil
}

// taxRate es la tasa de IVA de la categoría del producto.
func taxRate(ctx context.Context, db postgres.DBTX, prod *model.Product) (decimal.Decimal, error) {
	cat, err := postgres.NewCategoryStore(db).GetByID(ctx, prod.CategoryID)
	if err != nil {
		return decimal.Zero, err
	}
	tax, err := postgres.NewTaxStore(db).GetByID(ctx, cat.TaxID)
	if err != nil {
		return decimal.Zero, err
	}
	return tax.Rate, nil
}

// takeBatches saca el renglón de los lotes (FIFO) si el producto los
// controla, llena it.Batches y el costo del renglón (it.UnitCost: el
// promedio de lo que salió). Devuelve lo que salió sin lote y el costo
// promedio vigente, al que sale eso.
func takeBatches(ctx context.Context, tx postgres.DBTX, prod *model.Product,
	it *model.SaleItem) (left, avg decimal.Decimal, err error) {
	avg = decimal.Zero
	if c, err := postgres.NewCostStore(tx).Current(ctx, prod.ID); err == nil {
		avg = c.AvgCost
	} else if !errors.Is(err, store.ErrNotFound) {
		return decimal.Zero, decimal.Zero, err
	}

	left = it.Quantity
	cost := decimal.Zero // costo total de lo que salió
	if prod.ControlsBatches {
		takes, rest, err := postgres.NewBatchStore(tx).TakeFIFO(ctx, prod.ID, left)
		if err != nil {
			return decimal.Zero, decimal.Zero, err
		}
		for _, t := range takes {
			it.Batches = append(it.Batches, &model.SaleItemBatch{BatchID: t.BatchID, Quantity: t.Quantity, UnitCost: t.UnitCost})
			cost = cost.Add(t.Quantity.Mul(t.UnitCost))
		}
		left = rest
	}
	cost = cost.Add(left.Mul(avg))
	it.UnitCost = cost.Div(it.Quantity).Round(2)
	return left, avg, nil
}

// lineOrValidation: un error de ComputeTotals que nombra un renglón ("line
// 2: ...") pasa a LineError con el nombre del producto.
func lineOrValidation(sale *model.Sale, err error) error {
	if errors.Is(err, model.ErrSaleLineTotalZero) {
		for _, it := range sale.Items {
			if !it.LineTotal().IsPositive() {
				return &LineError{ProductID: it.ProductID, ProductName: it.ProductName,
					Err: fmt.Errorf("%w: %w", service.ErrValidation, model.ErrSaleLineTotalZero)}
			}
		}
	}
	return fmt.Errorf("%w: %w", service.ErrValidation, err)
}

// Get devuelve una venta para el ticket. service.ErrNotFound si no existe.
func (s *Service) Get(ctx context.Context, id int64) (*model.Sale, error) {
	if id <= 0 {
		return nil, service.ErrInvalidID
	}
	sale, err := postgres.NewSaleStore(s.db).Get(ctx, id)
	if err != nil {
		return nil, translate(err)
	}
	return sale, nil
}

// translate pasa los errores del store a los del service, como en conteos.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, service.ErrValidation), errors.Is(err, service.ErrConflict):
		return err
	case errors.Is(err, store.ErrNotFound):
		return service.ErrNotFound
	case errors.Is(err, store.ErrConflict):
		return fmt.Errorf("%w: %w", service.ErrConflict, err)
	}
	return err
}
