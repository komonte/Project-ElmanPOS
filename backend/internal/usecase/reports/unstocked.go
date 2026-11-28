package reports

// Vendidos sin stock (modo arranque, 2026-11-28): los productos que se
// vendieron sin que el sistema los tuviera y todavía no se contaron. Es la
// lista de qué contar primero; sale sola ordenada por rotación (los que más
// ventas tienen arriba). Un conteo confirmado del producto la resuelve: no
// hay que tocar nada más. Lo ven el admin y el repositor (permiso stock).

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Unstocked devuelve los productos vendidos sin stock que hay que contar.
func (s *Service) Unstocked(ctx context.Context) ([]model.UnstockedItem, error) {
	return postgres.NewReportStore(s.db).Unstocked(ctx)
}

// UnstockedFor dice cuánto de cada producto de ids se vendió sin stock y
// falta contar (los que no tienen nada no están). Para avisar al recibir
// una compra: conviene contar lo que hay antes de acomodar lo nuevo.
func (s *Service) UnstockedFor(ctx context.Context, ids []int64) (map[int64]decimal.Decimal, error) {
	return postgres.NewReportStore(s.db).UnstockedFor(ctx, ids)
}
