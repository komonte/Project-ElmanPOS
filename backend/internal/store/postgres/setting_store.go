package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// SettingStore lee la configuración del negocio (app_settings, 02 4.25).
type SettingStore struct {
	db DBTX
}

func NewSettingStore(db DBTX) *SettingStore {
	return &SettingStore{db: db}
}

// Decimal lee un ajuste que es un número (por ejemplo max_sale_weight_kg).
//   - ErrNotFound: la clave no está.
//   - un error común si el valor no es un número: el ajuste está mal cargado.
func (s *SettingStore) Decimal(ctx context.Context, key string) (decimal.Decimal, error) {
	var kind, text string
	err := s.db.QueryRowContext(ctx, `
		SELECT jsonb_typeof(value), value #>> '{}' FROM app_settings WHERE key = $1;`, key).Scan(&kind, &text)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return decimal.Zero, store.ErrNotFound
		}
		return decimal.Zero, err
	}
	if kind != "number" {
		return decimal.Zero, fmt.Errorf("setting %s is not a number: %s", key, kind)
	}
	return decimal.NewFromString(text)
}
