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

// Bool lee un ajuste que es sí o no (por ejemplo startup_mode).
//   - ErrNotFound: la clave no está.
//   - un error común si el valor no es true/false: el ajuste está mal cargado.
func (s *SettingStore) Bool(ctx context.Context, key string) (bool, error) {
	var kind, text string
	err := s.db.QueryRowContext(ctx, `
		SELECT jsonb_typeof(value), value #>> '{}' FROM app_settings WHERE key = $1;`, key).Scan(&kind, &text)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, store.ErrNotFound
		}
		return false, err
	}
	if kind != "boolean" {
		return false, fmt.Errorf("setting %s is not a boolean: %s", key, kind)
	}
	return text == "true", nil
}

// SetBool guarda un ajuste sí o no. ErrNotFound si la clave no existe: los
// ajustes los crea una migración, con su descripción.
func (s *SettingStore) SetBool(ctx context.Context, key string, value bool) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE app_settings SET value = to_jsonb($2::boolean), updated_at = now() WHERE key = $1;`, key, value)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}
