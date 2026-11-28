// Package sales es el caso de uso F-07 (venta atómica) con F-10 (venta por
// peso): cobra el carrito de la caja y descuenta el stock, todo o nada
// (BR-04). La anulación (F-08) está en void.go.
//
// Decisiones (2026-11-26):
//   - Se vende en la sesión abierta de la única caja del local activo. Sin
//     caja abierta no se vende (BR-02).
//   - Solo productos activos y que tengan precio.
//   - Por unidad, cantidades enteras; por peso, kg con hasta 3 decimales y
//     no más que app_settings max_sale_weight_kg.
//   - Sin stock suficiente en el local, la venta se rechaza entera: el stock
//     inicial se carga antes con un conteo.
//   - Salvo en modo arranque (StartupModeKey, 2026-11-28): el local carga su
//     stock de a poco y no se puede frenar la caja. Lo que el sistema no
//     tiene se vende igual: sale lo que hay (el saldo queda en 0, nunca
//     negativo) y el resto queda en el renglón (Uncovered), sin movimiento,
//     para contarlo. Ver reports.Unstocked.
//   - Precio vigente y IVA de la categoría, copiados en el renglón (BR-03).
//   - Artículo sin registrar (producto con open_price, migración 0022): el
//     precio lo escribe la caja en el renglón y no mueve stock (no tiene
//     stock propio, ni lotes, ni costo: el margen lo avisa como sin costo).
//     Si se escaneó un código que no estaba, queda en el renglón
//     (ScannedCode) para darlo de alta después.
//   - De un producto con lotes, sale de los lotes en orden FIFO; lo que los
//     lotes no cubren sale sin lote. Cada lote a su costo y sin lote al costo
//     promedio vigente. El promedio no cambia.
//   - Sin descuentos. Los pagos tienen que cubrir el total; solo el efectivo
//     puede pasarse (el vuelto no se guarda).
//   - Número de venta por caja, sin repetirse entre sesiones.
//   - Combos (2026-11-28, migración 0023): el renglón es el combo, con su
//     precio y el IVA de su categoría; el stock sale de sus componentes (la
//     cantidad del renglón por la de la receta), cada uno con sus
//     movimientos y lotes, y queda en sale_item_components. El costo del
//     renglón es lo que costaron sus componentes. Lo que piden los renglones
//     y los combos se suma por producto: 1 atado de 20 y 3 sueltos piden 23
//     sueltos. En modo arranque, lo que falta de un componente queda en su
//     Uncovered. Anular no cambia: devuelve cada movimiento de la venta.
//   - by es el usuario que cobra (F-13): queda en sales.created_by y en el
//     performed_by de los movimientos.
//
// Los errores salen como los del service genérico (service.ErrValidation,
// service.ErrConflict...), así la API y la web los traducen igual.
package sales

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// MaxWeightKey es el ajuste con el peso máximo por renglón (migración 0017).
const MaxWeightKey = "max_sale_weight_kg"

// StartupModeKey es el ajuste del modo arranque (migración 0021). Si no
// está, es como si estuviera apagado.
const StartupModeKey = "startup_mode"

// defaultMaxWeight se usa si el ajuste no está (alguien lo borró).
var defaultMaxWeight = decimal.NewFromInt(50)

var (
	ErrRegisterClosed  = fmt.Errorf("the cash register is not open: %w", service.ErrConflict)
	ErrUnknownProduct  = fmt.Errorf("product does not exist: %w", service.ErrValidation)
	ErrProductInactive = fmt.Errorf("product is inactive: %w", service.ErrConflict)
	// ErrComboEmpty: un combo sin componentes no tiene de dónde sacar stock.
	ErrComboEmpty = fmt.Errorf("%w: %w", service.ErrConflict, model.ErrComboWithoutComponents)
	// ErrComboComponent: un componente del combo ya no sirve (se desactivó,
	// pasó a ser combo...). Envuelve el error del model; LineError.Component
	// dice cuál.
	ErrComboComponent = fmt.Errorf("a component of the combo cannot be sold: %w", service.ErrConflict)
	ErrNoPrice        = fmt.Errorf("product has no price: %w", service.ErrConflict)
	ErrNotEnoughStock = fmt.Errorf("not enough stock: %w", service.ErrConflict)
	// ErrOpenPriceMissing: un artículo sin registrar sin precio escrito.
	ErrOpenPriceMissing = fmt.Errorf("write the price of the unregistered item: %w", service.ErrValidation)
)

// maxScannedCode es el largo de sale_items.scanned_code.
const maxScannedCode = 40

// LineError es un problema con un renglón: dice qué producto es, para el
// mensaje, y envuelve el error puntual (ErrNoPrice, ErrNotEnoughStock, un
// error de cantidad del model...). Con ErrNotEnoughStock, Available es lo
// que hay en el local.
//
// En un combo, Component es el componente con el problema (el que no
// alcanza, el que se desactivó); vacío en los demás renglones.
type LineError struct {
	ProductID   int64
	ProductName string
	Component   string
	Available   decimal.Decimal
	Err         error
}

func (e *LineError) Error() string {
	name := e.ProductName
	if e.Component != "" {
		name += ": " + e.Component
	}
	if errors.Is(e.Err, ErrNotEnoughStock) {
		return fmt.Sprintf("%s: %v (there is %s)", name, e.Err, e.Available)
	}
	return fmt.Sprintf("%s: %v", name, e.Err)
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
func (s *Service) Checkout(ctx context.Context, by int64, in *model.Sale) (*model.Sale, error) {
	if in == nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, model.ErrNilSale)
	}
	sale := copyInput(in)
	sale.CreatedBy = &by
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
		startup, err := StartupMode(ctx, tx)
		if err != nil {
			return err
		}

		// Las recetas de los combos se leen antes de trabar: dicen qué stock
		// hay que trabar. Cambiar una receta solo afecta las ventas que vienen.
		comps, err := comboComponents(ctx, tx, sale.Items)
		if err != nil {
			return err
		}

		// Primero traba todo el stock (renglones y componentes), en orden de
		// id, y después lee.
		balances := postgres.NewStockBalanceStore(tx)
		ids := make([]int64, 0, len(sale.Items))
		for _, it := range sale.Items {
			ids = append(ids, it.ProductID)
			for _, c := range comps[it.ProductID] {
				ids = append(ids, c.ComponentID)
			}
		}
		slices.Sort(ids)
		for _, id := range slices.Compact(ids) {
			if _, err := balances.LockOnHand(ctx, id); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return &LineError{ProductID: id, Err: ErrUnknownProduct}
				}
				return err
			}
		}

		products := make(map[int64]*model.Product, len(sale.Items))
		var draws []*draw
		for i, it := range sale.Items {
			prod, lineDraws, err := priceLine(ctx, tx, it, maxWeight, comps[it.ProductID])
			if err != nil {
				return err
			}
			// Repetido solo un artículo sin registrar: el stock y los lotes
			// de los demás se cuentan por producto.
			if _, dup := products[it.ProductID]; dup && !prod.OpenPrice {
				return fmt.Errorf("%w: line %d: %w", service.ErrValidation, i+1, model.ErrSaleDuplicateProduct)
			}
			products[it.ProductID] = prod
			draws = append(draws, lineDraws...)
		}
		if err := checkStock(ctx, tx, draws, storeID, startup); err != nil {
			return err
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
		// cada renglón (y cada componente) y su costo.
		for _, d := range draws {
			if err := takeBatches(ctx, tx, d); err != nil {
				return err
			}
		}
		lineCosts(sale.Items, draws)
		if err := postgres.NewSaleStore(tx).Create(ctx, sale); err != nil {
			return err
		}

		movements := postgres.NewStockMovementStore(tx)
		move := func(productID int64, batch *int64, qty, unitCost decimal.Decimal) error {
			_, err := movements.Create(ctx, &model.StockMovement{
				ProductID:    productID,
				StoreID:      storeID,
				BatchID:      batch,
				MovementType: model.MovementSale,
				Quantity:     qty.Neg(),
				UnitCost:     unitCost,
				SaleID:       &sale.ID,
				PerformedBy:  &by,
			})
			return err
		}
		for _, d := range draws {
			for _, b := range d.batches {
				if err := move(d.prod.ID, &b.BatchID, b.Quantity, b.UnitCost); err != nil {
					return err
				}
			}
			// Lo no cubierto no tiene movimiento: el sistema nunca lo tuvo.
			if left := d.left.Sub(d.uncovered()); left.IsPositive() {
				if err := move(d.prod.ID, nil, left, d.avg); err != nil {
					return err
				}
			}
			if out := d.qty.Sub(d.uncovered()); out.IsPositive() {
				if _, err := balances.Add(ctx, d.prod.ID, storeID, out.Neg()); err != nil {
					return err
				}
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
		// El precio solo se usa si es un artículo sin registrar (priceLine);
		// a los demás se les pone el vigente.
		code := strings.TrimSpace(it.ScannedCode)
		if n, err := model.NormalizeBarcode(code); err == nil {
			code = n // igual que en product_codes, para encontrarlo al darlo de alta
		}
		if len(code) > maxScannedCode {
			code = code[:maxScannedCode]
		}
		out.Items = append(out.Items, &model.SaleItem{ProductID: it.ProductID, Quantity: it.Quantity,
			UnitPrice: it.UnitPrice, ScannedCode: code})
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

// StartupMode dice si el modo arranque está prendido. Sin el ajuste,
// apagado.
func StartupMode(ctx context.Context, db postgres.DBTX) (bool, error) {
	on, err := postgres.NewSettingStore(db).Bool(ctx, StartupModeKey)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return on, err
}

// priceLine revisa un renglón (producto vendible, cantidad) y le pone
// precio, IVA y nombre. Devuelve también de dónde sale su stock: el mismo
// producto o, si es un combo, sus componentes (comps, su receta); un
// artículo sin registrar no saca de ningún lado. El stock lo revisa
// checkStock, con todo el carrito.
func priceLine(ctx context.Context, tx postgres.DBTX, it *model.SaleItem, maxWeight decimal.Decimal,
	comps []*model.ProductComponent) (*model.Product, []*draw, error) {
	products := postgres.NewProductStore(tx)
	prod, err := products.GetByID(ctx, it.ProductID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, &LineError{ProductID: it.ProductID, Err: ErrUnknownProduct}
	}
	if err != nil {
		return nil, nil, err
	}
	lineErr := func(err error) *LineError {
		return &LineError{ProductID: prod.ID, ProductName: prod.Name, Err: err}
	}
	if !prod.IsActive {
		return nil, nil, lineErr(ErrProductInactive)
	}
	if err := it.ValidateQuantity(prod.SaleType, maxWeight); err != nil {
		return nil, nil, lineErr(fmt.Errorf("%w: %w", service.ErrValidation, err))
	}
	if prod.OpenPrice {
		p, err := openPriceLine(ctx, tx, it, prod, lineErr)
		return p, nil, err
	}
	it.ScannedCode = "" // solo lo guarda un artículo sin registrar

	// De dónde sale: el producto mismo, o los componentes del combo (cada
	// uno, la cantidad del renglón por la de la receta).
	var draws []*draw
	if prod.SaleType != model.SaleTypeComposite {
		draws = []*draw{{item: it, prod: prod, qty: it.Quantity}}
	} else {
		if len(comps) == 0 {
			return nil, nil, lineErr(ErrComboEmpty)
		}
		it.Components = make([]*model.SaleItemComponent, 0, len(comps))
		for _, c := range comps {
			comp, err := products.GetByID(ctx, c.ComponentID)
			if err != nil {
				return nil, nil, err
			}
			qty := it.Quantity.Mul(c.Quantity)
			// La receta pudo quedar vieja (el componente se desactivó o pasó
			// a ser combo después de cargarla): se revisa en cada venta.
			if err := model.CheckComponent(prod, comp, c.Quantity); err != nil {
				e := lineErr(fmt.Errorf("%w: %w", ErrComboComponent, err))
				e.Component = comp.Name
				return nil, nil, e
			}
			sc := &model.SaleItemComponent{ProductID: comp.ID, Quantity: qty}
			it.Components = append(it.Components, sc)
			draws = append(draws, &draw{item: it, prod: comp, qty: qty, comp: sc})
		}
	}

	price, err := postgres.NewPriceStore(tx).Current(ctx, prod.ID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil, lineErr(ErrNoPrice)
	}
	if err != nil {
		return nil, nil, err
	}
	rate, err := taxRate(ctx, tx, prod)
	if err != nil {
		return nil, nil, err
	}

	it.UnitPrice, it.TaxRate = price.UnitPrice, rate
	it.ProductName, it.SaleType = prod.Name, prod.SaleType
	return prod, draws, nil
}

// draw es una salida de stock de un renglón: del producto del renglón o de
// un componente del combo (comp). takeBatches llena los lotes y costos.
type draw struct {
	item *model.SaleItem
	prod *model.Product           // de quién sale el stock
	qty  decimal.Decimal          // cuánto
	comp *model.SaleItemComponent // nil si sale del producto del renglón

	batches []*model.SaleItemBatch
	left    decimal.Decimal // lo que salió sin lote (incluye lo no cubierto)
	avg     decimal.Decimal // costo promedio vigente, al que sale left
	cost    decimal.Decimal // costo total de qty
}

// uncovered es cuánto de la salida el sistema no tenía (modo arranque).
func (d *draw) uncovered() decimal.Decimal {
	if d.comp != nil {
		return d.comp.Uncovered
	}
	return d.item.Uncovered
}

func (d *draw) setUncovered(q decimal.Decimal) {
	if d.comp != nil {
		d.comp.Uncovered = q
	} else {
		d.item.Uncovered = q
	}
}

// comboComponents lee la receta de los combos del carrito, por combo. Los
// productos que no son combos (o no existen) no están en el mapa.
func comboComponents(ctx context.Context, tx postgres.DBTX, items []*model.SaleItem) (map[int64][]*model.ProductComponent, error) {
	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.ProductID
	}
	byPack, err := postgres.NewProductComponentStore(tx).ListByPacks(ctx, ids)
	if err != nil {
		return nil, err
	}
	// Solo cuentan las recetas de los combos: un producto que dejó de serlo
	// puede tener filas viejas.
	for id := range byPack {
		p, err := postgres.NewProductStore(tx).GetByID(ctx, id)
		if err != nil {
			return nil, err
		}
		if p.SaleType != model.SaleTypeComposite {
			delete(byPack, id)
		}
	}
	return byPack, nil
}

// checkStock revisa el stock de todo el carrito, ya trabado: lo que piden
// los renglones y los combos se suma por producto (1 atado de 20 y 3
// sueltos piden 23 sueltos). Lo que hay se reparte en el orden del carrito.
// Sin alcanzar, la venta se rechaza (el error nombra el renglón y, si es un
// combo, el componente); en modo arranque, lo que falta queda en el
// Uncovered de esa salida.
func checkStock(ctx context.Context, tx postgres.DBTX, draws []*draw, storeID int64, startup bool) error {
	balances := postgres.NewStockBalanceStore(tx)
	left := map[int64]decimal.Decimal{} // lo que queda de cada producto
	for _, d := range draws {
		have, ok := left[d.prod.ID]
		if !ok {
			var err error
			if have, err = balances.OnHandAt(ctx, d.prod.ID, storeID); err != nil {
				return err
			}
			have = decimal.Max(have, decimal.Zero)
		}
		if d.qty.GreaterThan(have) {
			if !startup {
				e := &LineError{ProductID: d.item.ProductID, ProductName: d.item.ProductName,
					Err: ErrNotEnoughStock, Available: have}
				if d.comp != nil {
					e.Component = d.prod.Name
				}
				return e
			}
			d.setUncovered(d.qty.Sub(have))
		}
		left[d.prod.ID] = decimal.Max(have.Sub(d.qty), decimal.Zero)
	}
	return nil
}

// lineCosts pone el costo de cada renglón con stock: el de su producto o, en
// un combo, la suma de lo que salió de sus componentes (cada componente
// guarda su costo por unidad).
func lineCosts(items []*model.SaleItem, draws []*draw) {
	total := map[*model.SaleItem]decimal.Decimal{}
	for _, d := range draws {
		total[d.item] = total[d.item].Add(d.cost)
		if d.comp != nil {
			d.comp.UnitCost = d.cost.Div(d.qty).Round(2)
			d.item.Batches = append(d.item.Batches, d.batches...) // el lote dice de qué producto es
		} else {
			d.item.Batches = d.batches
		}
	}
	for _, it := range items {
		if cost, ok := total[it]; ok {
			it.UnitCost = cost.Div(it.Quantity).Round(2)
		}
	}
}

// openPriceLine arma el renglón de un artículo sin registrar: el precio que
// escribió la caja (con dos decimales), el IVA de la categoría, costo 0 y
// sin stock.
func openPriceLine(ctx context.Context, tx postgres.DBTX, it *model.SaleItem, prod *model.Product,
	lineErr func(error) *LineError) (*model.Product, error) {
	price := it.UnitPrice.Round(2)
	if !price.IsPositive() {
		return nil, lineErr(ErrOpenPriceMissing)
	}
	rate, err := taxRate(ctx, tx, prod)
	if err != nil {
		return nil, err
	}
	it.UnitPrice, it.TaxRate, it.UnitCost, it.Uncovered = price, rate, decimal.Zero, decimal.Zero
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

// takeBatches saca la salida d de los lotes (FIFO) si el producto los
// controla, y llena d.batches, lo que salió sin lote (d.left), el costo
// promedio vigente al que sale eso (d.avg) y el costo total (d.cost).
func takeBatches(ctx context.Context, tx postgres.DBTX, d *draw) error {
	d.avg = decimal.Zero
	if c, err := postgres.NewCostStore(tx).Current(ctx, d.prod.ID); err == nil {
		d.avg = c.AvgCost
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}

	d.left = d.qty
	cost := decimal.Zero // costo total de lo que salió
	if d.prod.ControlsBatches {
		takes, rest, err := postgres.NewBatchStore(tx).TakeFIFO(ctx, d.prod.ID, d.left)
		if err != nil {
			return err
		}
		for _, t := range takes {
			d.batches = append(d.batches, &model.SaleItemBatch{BatchID: t.BatchID, Quantity: t.Quantity, UnitCost: t.UnitCost})
			cost = cost.Add(t.Quantity.Mul(t.UnitCost))
		}
		d.left = rest
	}
	d.cost = cost.Add(d.left.Mul(d.avg))
	return nil
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

// Option es un producto encontrado por el buscador de la caja, con lo que
// la pantalla necesita para agregarlo al carrito.
type Option struct {
	ID       int64          `json:"id"`
	Name     string         `json:"name"`
	SKU      string         `json:"sku"`
	SaleType model.SaleType `json:"sale_type"`
	// Price es el precio vigente (por kg si es a granel); nil si no tiene:
	// no se puede vender.
	Price *decimal.Decimal `json:"price"`
	// OnHand es el stock del local activo.
	OnHand decimal.Decimal `json:"on_hand"`
	// OpenPrice: artículo sin registrar, el precio se escribe en la caja.
	OpenPrice bool `json:"open_price"`
}

// Search busca productos para vender por nombre, SKU o código de barras
// (escanear): los activos, hasta 20, con su precio y su stock en el local
// activo. El stock de un combo es cuántos alcanzan con el de sus componentes.
//   - service.ErrConflict: no hay un único local activo.
func (s *Service) Search(ctx context.Context, query string) ([]Option, error) {
	storeID, err := postgres.NewStoreStore(s.db).ActiveID(ctx)
	if err != nil {
		return nil, translate(err)
	}
	found, err := postgres.NewProductStore(s.db).SearchByName(ctx, query)
	if err != nil {
		return nil, err
	}
	var packIDs []int64
	for _, p := range found {
		if p.SaleType == model.SaleTypeComposite {
			packIDs = append(packIDs, p.ID)
		}
	}
	comps, err := postgres.NewProductComponentStore(s.db).ListByPacks(ctx, packIDs)
	if err != nil {
		return nil, err
	}
	prices := postgres.NewPriceStore(s.db)
	balances := postgres.NewStockBalanceStore(s.db)
	options := make([]Option, 0, len(found))
	for _, p := range found {
		o := Option{ID: p.ID, Name: p.Name, SKU: p.SKU, SaleType: p.SaleType, OpenPrice: p.OpenPrice}
		price, err := prices.Current(ctx, p.ID)
		switch {
		case err == nil:
			o.Price = &price.UnitPrice
		case !errors.Is(err, store.ErrNotFound):
			return nil, err
		}
		if p.SaleType == model.SaleTypeComposite {
			o.OnHand, err = comboOnHand(ctx, balances, comps[p.ID], storeID)
		} else {
			o.OnHand, err = balances.OnHandAt(ctx, p.ID, storeID)
		}
		if err != nil {
			return nil, err
		}
		options = append(options, o)
	}
	return options, nil
}

// comboOnHand es cuántos combos enteros alcanzan con el stock de sus
// componentes: el mínimo de (stock del componente / cantidad de la receta).
// Sin componentes, 0.
func comboOnHand(ctx context.Context, balances *postgres.StockBalanceStore, comps []*model.ProductComponent,
	storeID int64) (decimal.Decimal, error) {
	var out decimal.Decimal
	for i, c := range comps {
		have, err := balances.OnHandAt(ctx, c.ComponentID, storeID)
		if err != nil {
			return decimal.Zero, err
		}
		n := decimal.Max(have, decimal.Zero).Div(c.Quantity).Floor()
		if i == 0 || n.LessThan(out) {
			out = n
		}
	}
	return out, nil
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

// OpenItems son los artículos sin registrar que la caja puede usar (activos,
// por nombre): cuando un código no está, se ofrece cobrarlo como uno de
// ellos, con el precio escrito.
func (s *Service) OpenItems(ctx context.Context) ([]Option, error) {
	found, err := postgres.NewProductStore(s.db).OpenPriceProducts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Option, 0, len(found))
	for _, p := range found {
		out = append(out, Option{ID: p.ID, Name: p.Name, SKU: p.SKU, SaleType: p.SaleType, OpenPrice: true})
	}
	return out, nil
}
