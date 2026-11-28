package model

import (
	"errors"
	"time"

	"github.com/shopspring/decimal"
)

// Reportes de ventas y caja (R-06, R-07). Decisiones (2026-11-26):
//   - Un período son días enteros en la hora del local: "desde" y "hasta"
//     incluidos. Sin fechas, es hoy. Hasta 366 días.
//   - Cómo se agrupa: un día va por hora, hasta 62 días por día, y más por
//     mes.
//   - Los tramos se arman acá, en la zona del local (medianoche, cada hora,
//     el 1 de cada mes), y la base solo dice en qué tramo cae cada venta
//     (width_bucket). Así el SQL no depende de la zona horaria.
//   - El margen es sin IVA: los costos se guardan sin IVA y los precios con
//     IVA. Lo vendido sin costo cargado queda fuera del margen y se avisa.

const (
	PeriodDateLayout = "2006-01-02" // el de <input type="date">
	PeriodMaxDays    = 366
	periodMaxDaily   = 62 // más días que esto, por mes
)

var (
	ErrInvalidPeriodDate = errors.New("dates must look like 2026-11-26")
	ErrPeriodReversed    = errors.New("the start date is after the end date")
	ErrPeriodTooLong     = errors.New("the period cannot be longer than 366 days")
)

// Granularity es el tamaño de cada tramo de un reporte.
type Granularity string

const (
	ByHour  Granularity = "hour"
	ByDay   Granularity = "day"
	ByMonth Granularity = "month"
)

// Period son los días de un reporte, en la hora del local.
type Period struct {
	From time.Time // medianoche del primer día, en Loc
	To   time.Time // medianoche del último día (incluido), en Loc
	Loc  *time.Location
}

// ParsePeriod lee "desde" y "hasta" (2026-11-26). Vacíos son hoy (now en
// loc); si falta uno, es el mismo día que el otro.
func ParsePeriod(from, to string, loc *time.Location, now time.Time) (Period, error) {
	day := func(s string) (time.Time, error) {
		t, err := time.ParseInLocation(PeriodDateLayout, s, loc)
		if err != nil {
			return time.Time{}, ErrInvalidPeriodDate
		}
		return t, nil
	}
	today := midnight(now.In(loc))
	p := Period{From: today, To: today, Loc: loc}
	var err error
	switch {
	case from == "" && to == "":
	case from == "":
		if p.To, err = day(to); err != nil {
			return Period{}, err
		}
		p.From = p.To
	case to == "":
		if p.From, err = day(from); err != nil {
			return Period{}, err
		}
		p.To = p.From
	default:
		if p.From, err = day(from); err != nil {
			return Period{}, err
		}
		if p.To, err = day(to); err != nil {
			return Period{}, err
		}
	}
	if p.From.After(p.To) {
		return Period{}, ErrPeriodReversed
	}
	if p.Days() > PeriodMaxDays {
		return Period{}, ErrPeriodTooLong
	}
	return p, nil
}

func midnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// Start es el primer instante del período.
func (p Period) Start() time.Time { return p.From }

// End es el primer instante después del período: la medianoche siguiente
// al último día.
func (p Period) End() time.Time { return p.To.AddDate(0, 0, 1) }

// Days es cuántos días tiene (desde y hasta incluidos).
func (p Period) Days() int {
	// Por fecha, no por horas: un día con cambio de hora no tiene 24.
	a := time.Date(p.From.Year(), p.From.Month(), p.From.Day(), 0, 0, 0, 0, time.UTC)
	b := time.Date(p.To.Year(), p.To.Month(), p.To.Day(), 0, 0, 0, 0, time.UTC)
	return int(b.Sub(a).Hours()/24) + 1
}

// Granularity dice cómo se agrupa: un día por hora, hasta 62 días por día,
// más por mes.
func (p Period) Granularity() Granularity {
	switch d := p.Days(); {
	case d == 1:
		return ByHour
	case d <= periodMaxDaily:
		return ByDay
	default:
		return ByMonth
	}
}

// Buckets son los comienzos de cada tramo, en orden, en la hora del local.
// El primero es Start; el último termina en End. Por mes, el primero
// empieza en From aunque no sea el 1.
func (p Period) Buckets() []time.Time {
	var out []time.Time
	end := p.End()
	switch p.Granularity() {
	case ByHour:
		for h := 0; ; h++ {
			// Por reloj, no sumando horas: así un día con cambio de hora
			// tiene sus 23 o 25 tramos bien nombrados.
			t := time.Date(p.From.Year(), p.From.Month(), p.From.Day(), h, 0, 0, 0, p.Loc)
			if !t.Before(end) || h >= 24 {
				break
			}
			if len(out) > 0 && !t.After(out[len(out)-1]) {
				continue // una hora que se repite al atrasar el reloj
			}
			out = append(out, t)
		}
	case ByDay:
		for t := p.From; t.Before(end); t = t.AddDate(0, 0, 1) {
			out = append(out, t)
		}
	default:
		out = append(out, p.From)
		for t := time.Date(p.From.Year(), p.From.Month()+1, 1, 0, 0, 0, 0, p.Loc); t.Before(end); t = t.AddDate(0, 1, 0) {
			out = append(out, t)
		}
	}
	return out
}

// Margin es el margen bruto de lo vendido (R-07), sin IVA.
type Margin struct {
	// Net es lo vendido sin IVA, solo de los renglones con costo.
	Net decimal.Decimal `json:"net"`
	// Cost es lo que costó eso (unit_cost × cantidad).
	Cost decimal.Decimal `json:"cost"`
	// NoCost es lo vendido (con IVA) de renglones sin costo cargado: no
	// entra en el margen.
	NoCost decimal.Decimal `json:"no_cost"`
}

// Amount es Net − Cost.
func (m Margin) Amount() decimal.Decimal { return m.Net.Sub(m.Cost) }

// Percent es el margen sobre lo vendido sin IVA, con un decimal. 0 si no
// hubo ventas con costo.
func (m Margin) Percent() decimal.Decimal {
	if m.Net.IsZero() {
		return decimal.Zero
	}
	return m.Amount().Mul(decimal.NewFromInt(100)).Div(m.Net).Round(1)
}

// Add suma dos márgenes.
func (m Margin) Add(o Margin) Margin {
	return Margin{Net: m.Net.Add(o.Net), Cost: m.Cost.Add(o.Cost), NoCost: m.NoCost.Add(o.NoCost)}
}

// SalesTally es lo vendido en un tramo (R-06): solo ventas COMPLETED.
type SalesTally struct {
	// Bucket es el número de tramo: el índice en Period.Buckets().
	Bucket int             `json:"bucket"`
	Count  int             `json:"count"`
	Total  decimal.Decimal `json:"total"`
	// Units son las unidades de los productos por unidad; Kg, los kilos de
	// los que se venden por peso. Van separados: sumarlos no dice nada.
	Units decimal.Decimal `json:"units"`
	Kg    decimal.Decimal `json:"kg"`
}

// PaymentTally son los pagos de un medio en un tramo (R-06).
type PaymentTally struct {
	Bucket int             `json:"bucket"`
	Method string          `json:"method"`
	Count  int             `json:"count"`
	Amount decimal.Decimal `json:"amount"`
}

// SessionTally es una sesión de caja cerrada con lo que hace falta para el
// reporte de caja y margen (R-07).
type SessionTally struct {
	Session     *CashierSession `json:"session"`
	Sales       int             `json:"sales"`
	Total       decimal.Decimal `json:"total"`
	Voided      int             `json:"voided"`
	VoidedTotal decimal.Decimal `json:"voided_total"`
	// Paid son los pagos de las ventas COMPLETED por medio, y Declared lo
	// que se contó al cerrar: con eso, NewCloseSummary da el arqueo.
	Paid     map[string]decimal.Decimal `json:"paid"`
	Declared []*CashAudit               `json:"declared"`
	Margin   Margin                     `json:"margin"`
}

// RestockItem es un producto del reporte de reposición (R-01).
type RestockItem struct {
	ProductID    int64            `json:"product_id"`
	SKU          string           `json:"sku"`
	Name         string           `json:"name"`
	SaleType     SaleType         `json:"sale_type"`
	SupplierID   *int64           `json:"supplier_id"`
	SupplierName *string          `json:"supplier_name"`
	Quantity     decimal.Decimal  `json:"quantity"` // el stock de hoy
	ReorderPoint decimal.Decimal  `json:"reorder_point"`
	MaxStock     *decimal.Decimal `json:"max_stock"`
	// Sold son las unidades (o kg) vendidas en los últimos 30 días.
	Sold decimal.Decimal `json:"sold"`
}

// Suggested es cuánto pedir: hasta el máximo si está cargado; si no, lo que
// se vende en un mes (lo vendido en 30 días) menos el stock. Por unidad se
// redondea para arriba; por peso, a gramos. 0 si no hace falta o no hay con
// qué calcularlo.
func (it RestockItem) Suggested() decimal.Decimal {
	target := it.Sold
	if it.MaxStock != nil {
		target = *it.MaxStock
	}
	s := target.Sub(it.Quantity)
	if !s.IsPositive() {
		return decimal.Zero
	}
	if it.SaleType == SaleTypeWeight {
		return s.Round(3)
	}
	return s.Ceil()
}

// ExpiringBatch es un lote con stock que vence pronto o ya venció (R-02).
type ExpiringBatch struct {
	BatchID       int64           `json:"batch_id"`
	BatchNumber   string          `json:"batch_number"`
	ProductID     int64           `json:"product_id"`
	SKU           string          `json:"sku"`
	Name          string          `json:"name"`
	ProductActive bool            `json:"product_active"`
	SaleType      SaleType        `json:"sale_type"`
	Expiry        string          `json:"expiry"` // 2026-11-26
	Remaining     decimal.Decimal `json:"remaining"`
	UnitCost      decimal.Decimal `json:"unit_cost"`
}

// Value es lo que vale lo que queda del lote, a su costo.
func (b ExpiringBatch) Value() decimal.Decimal {
	return b.Remaining.Mul(b.UnitCost).Round(2)
}

// UnstockedItem es un producto vendido sin stock en modo arranque que todavía
// no se contó: la suma de lo que faltó en sus ventas sin anular, desde la
// última vez que un conteo confirmado lo midió.
type UnstockedItem struct {
	ProductID int64           `json:"product_id"`
	SKU       string          `json:"sku"`
	Name      string          `json:"name"`
	SaleType  SaleType        `json:"sale_type"`
	Uncovered decimal.Decimal `json:"uncovered"` // lo vendido sin stock
	Sales     int             `json:"sales"`     // en cuántas ventas
	FirstSold string          `json:"first_sold"`
	LastSold  string          `json:"last_sold"`
	OnHand    decimal.Decimal `json:"on_hand"` // el stock de hoy en el sistema
}

// UnregisteredCode es un código que la caja escaneó, no estaba y se cobró
// como artículo sin registrar (migración 0022), y que todavía no es de ningún
// producto: hay que darlo de alta.
type UnregisteredCode struct {
	Code      string          `json:"code"`
	Sales     int             `json:"sales"`    // en cuántas ventas sin anular
	Quantity  decimal.Decimal `json:"quantity"` // unidades vendidas en total
	LastPrice decimal.Decimal `json:"last_price"`
	LastSold  string          `json:"last_sold"`
	SoldAs    string          `json:"sold_as"` // el artículo sin registrar de la última venta
}
