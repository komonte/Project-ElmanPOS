// Package settings es la configuración del negocio que el admin cambia desde
// la pantalla (Administración > Configuración). Por ahora, el modo arranque.
//
// Decisiones (2026-11-28):
//   - Los ajustes viven en app_settings y los crea una migración, con su
//     descripción; acá solo se leen y se cambian.
//   - Cada cambio queda en audit_log con quién lo hizo (record_id 0: la
//     clave es texto y va en new_data).
//   - Solo el admin (permiso settings, en la ruta).
package settings

import (
	"context"
	"errors"
	"fmt"

	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sales"
)

// Settings es lo que muestra la pantalla.
type Settings struct {
	// StartupMode: la caja vende aunque el sistema no tenga el stock; lo que
	// falta queda en la lista de vendidos sin stock, para contarlo.
	StartupMode bool `json:"startup_mode"`
}

type Service struct {
	db postgres.DBTX
	tx postgres.TxRunner
}

func New(db postgres.DBTX, tx postgres.TxRunner) *Service {
	return &Service{db: db, tx: tx}
}

// Get lee la configuración.
func (s *Service) Get(ctx context.Context) (*Settings, error) {
	on, err := sales.StartupMode(ctx, s.db)
	if err != nil {
		return nil, err
	}
	return &Settings{StartupMode: on}, nil
}

// SetStartupMode prende o apaga el modo arranque; by es quién.
//   - service.ErrNotFound: el ajuste no está (falta la migración 0021).
func (s *Service) SetStartupMode(ctx context.Context, by int64, on bool) error {
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		if err := postgres.NewSettingStore(tx).SetBool(ctx, sales.StartupModeKey, on); err != nil {
			return err
		}
		_, err := postgres.NewAuditLogStore(tx).Create(ctx, postgres.AuditEntry{
			UserID:  &by,
			Table:   "app_settings",
			Action:  "UPDATE",
			NewData: map[string]any{"key": sales.StartupModeKey, "value": on},
		})
		return err
	})
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("%w: setting %s", service.ErrNotFound, sales.StartupModeKey)
	}
	return err
}
