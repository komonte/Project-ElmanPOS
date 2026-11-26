package reports

// Reportes de stock: reposición (R-01) y por vencer (R-02). Los ven el
// admin y el repositor (permiso stock). Decisiones (2026-11-26):
//   - Reposición: dos secciones. "Bajo el punto de pedido", agrupada por
//     proveedor preferido (sin proveedor al final) y dentro de cada grupo
//     el más crítico primero (stock / punto de pedido). "Se agotaron":
//     productos sin punto de pedido que se vendieron en los últimos 30 días
//     y no tienen stock, para que se les cargue uno.
//   - Por vencer: los días salen de app_settings.expiry_warning_days (30 si
//     falta o está mal); la página puede mirar otros sin cambiarlo.

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

const (
	// SoldDays son los días de ventas que miran la reposición y la cantidad
	// sugerida (lo que se vende en un mes).
	SoldDays = 30
	// DefaultExpiryDays es el aviso de vencimiento si el ajuste falta.
	DefaultExpiryDays = 30
	// MaxExpiryDays es lo más lejos que se puede mirar.
	MaxExpiryDays = 366
)

// RestockGroup son los productos de un proveedor (o sin proveedor).
type RestockGroup struct {
	SupplierID   *int64              `json:"supplier_id"`
	SupplierName string              `json:"supplier_name"` // vacío: sin proveedor
	Items        []model.RestockItem `json:"items"`
}

// Restock es el reporte de reposición (R-01).
type Restock struct {
	Below  []RestockGroup      `json:"below"`   // bajo el punto de pedido
	RanOut []model.RestockItem `json:"ran_out"` // se agotaron, sin punto de pedido
}

// Count es cuántos productos hay para reponer.
func (r *Restock) Count() int {
	n := len(r.RanOut)
	for _, g := range r.Below {
		n += len(g.Items)
	}
	return n
}

// Restock arma el reporte de reposición con las ventas de los últimos
// SoldDays días antes de now.
func (s *Service) Restock(ctx context.Context, now time.Time) (*Restock, error) {
	items, err := postgres.NewReportStore(s.db).Restock(ctx, now.AddDate(0, 0, -SoldDays))
	if err != nil {
		return nil, err
	}
	out := &Restock{}
	groups := map[int64]*RestockGroup{}
	var none *RestockGroup
	for _, it := range items {
		if it.ReorderPoint.IsZero() {
			out.RanOut = append(out.RanOut, it)
			continue
		}
		var g *RestockGroup
		if it.SupplierID == nil {
			if none == nil {
				none = &RestockGroup{}
			}
			g = none
		} else {
			if groups[*it.SupplierID] == nil {
				groups[*it.SupplierID] = &RestockGroup{SupplierID: it.SupplierID, SupplierName: *it.SupplierName}
			}
			g = groups[*it.SupplierID]
		}
		g.Items = append(g.Items, it)
	}
	for _, g := range groups {
		out.Below = append(out.Below, *g)
	}
	sort.Slice(out.Below, func(i, j int) bool {
		return strings.ToLower(out.Below[i].SupplierName) < strings.ToLower(out.Below[j].SupplierName)
	})
	if none != nil {
		out.Below = append(out.Below, *none)
	}
	for _, g := range out.Below {
		sort.SliceStable(g.Items, func(i, j int) bool {
			a, b := critical(g.Items[i]), critical(g.Items[j])
			if !a.Equal(b) {
				return a.LessThan(b)
			}
			return g.Items[i].Name < g.Items[j].Name
		})
	}
	sort.Slice(out.RanOut, func(i, j int) bool { return out.RanOut[i].Name < out.RanOut[j].Name })
	return out, nil
}

// critical es stock / punto de pedido: cuanto más chico, más urgente.
func critical(it model.RestockItem) decimal.Decimal {
	return it.Quantity.Div(it.ReorderPoint)
}

// ExpiringRow es un lote del reporte con los días que le quedan (negativo:
// hace cuántos días venció).
type ExpiringRow struct {
	model.ExpiringBatch
	DaysLeft int `json:"days_left"`
}

// Expiring es el reporte de lotes por vencer (R-02).
type Expiring struct {
	Days         int             `json:"days"`
	Expired      []ExpiringRow   `json:"expired"` // ya vencidos
	Soon         []ExpiringRow   `json:"soon"`    // vencen dentro de Days días
	ExpiredValue decimal.Decimal `json:"expired_value"`
	SoonValue    decimal.Decimal `json:"soon_value"`
}

// ExpiryDays son los días de aviso de vencimiento configurados
// (expiry_warning_days). Si falta o está mal, DefaultExpiryDays (y se
// avisa en el log si está mal).
func (s *Service) ExpiryDays(ctx context.Context) int {
	v, err := postgres.NewSettingStore(s.db).Decimal(ctx, "expiry_warning_days")
	switch {
	case errors.Is(err, store.ErrNotFound):
		return DefaultExpiryDays
	case err != nil || !v.IsInteger() || v.Sign() <= 0 || v.IntPart() > MaxExpiryDays:
		slog.Warn("bad expiry_warning_days, using the default", "value", v, "error", err)
		return DefaultExpiryDays
	}
	return int(v.IntPart())
}

// Expiring arma el reporte de lotes vencidos y por vencer dentro de days
// días desde today (la fecha del local).
func (s *Service) Expiring(ctx context.Context, today time.Time, days int) (*Expiring, error) {
	day := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	until := day.AddDate(0, 0, days).Format(time.DateOnly)
	batches, err := postgres.NewReportStore(s.db).Expiring(ctx, until)
	if err != nil {
		return nil, err
	}
	out := &Expiring{Days: days}
	for _, b := range batches {
		exp, err := time.Parse(time.DateOnly, b.Expiry)
		if err != nil {
			return nil, err
		}
		row := ExpiringRow{ExpiringBatch: b, DaysLeft: int(exp.Sub(day).Hours() / 24)}
		if row.DaysLeft < 0 {
			out.Expired = append(out.Expired, row)
			out.ExpiredValue = out.ExpiredValue.Add(b.Value())
		} else {
			out.Soon = append(out.Soon, row)
			out.SoonValue = out.SoonValue.Add(b.Value())
		}
	}
	return out, nil
}
