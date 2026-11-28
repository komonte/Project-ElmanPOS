// Package customers son los clientes para vender fiado (2026-11-28,
// migración 0025). Reglas del dato en model.Customer.
//
// Decisiones:
//   - Dar de alta: la caja (permiso sell, al fiar) o el admin. Editar,
//     desactivar y reactivar: el admin (permiso customers, en la ruta).
//   - Cada cambio queda en audit_log con quién: un cliente tiene deuda.
//
// Los errores salen como los del service genérico (service.ErrValidation,
// service.ErrNotFound...), así la API y la web los traducen igual.
package customers

import (
	"context"
	"errors"
	"fmt"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// ListLimit es cuántos clientes muestra la tabla.
const ListLimit = 200

// ErrNameTaken: ya hay un cliente con ese nombre.
var ErrNameTaken = fmt.Errorf("%w: %w", service.ErrConflict, postgres.ErrCustomerNameTaken)

type Service struct {
	db postgres.DBTX
	tx postgres.TxRunner
}

func New(db postgres.DBTX, tx postgres.TxRunner) *Service {
	return &Service{db: db, tx: tx}
}

// List devuelve los clientes que coinciden con query (vacía: todos),
// activos primero, hasta ListLimit.
func (s *Service) List(ctx context.Context, query string) ([]*model.Customer, error) {
	return postgres.NewCustomerStore(s.db).List(ctx, query, ListLimit)
}

// Search busca activos por nombre o teléfono (la caja).
func (s *Service) Search(ctx context.Context, query string) ([]*model.Customer, error) {
	return postgres.NewCustomerStore(s.db).Search(ctx, query)
}

// Get devuelve un cliente. service.ErrNotFound si no existe.
func (s *Service) Get(ctx context.Context, id int64) (*model.Customer, error) {
	if id <= 0 {
		return nil, service.ErrInvalidID
	}
	c, err := postgres.NewCustomerStore(s.db).GetByID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, service.ErrNotFound
	}
	return c, err
}

// Create da de alta un cliente; by es quién.
//   - service.ErrValidation: los datos están mal (envuelve el del model).
//   - ErrNameTaken (service.ErrConflict): el nombre ya está.
func (s *Service) Create(ctx context.Context, by int64, c *model.Customer) (*model.Customer, error) {
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}
	out := &model.Customer{Name: c.Name, Phone: c.Phone, Notes: c.Notes, CreatedBy: &by}
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		if err := postgres.NewCustomerStore(tx).Create(ctx, out); err != nil {
			return err
		}
		return audit(ctx, tx, by, "INSERT", out)
	})
	if errors.Is(err, postgres.ErrCustomerNameTaken) {
		return nil, ErrNameTaken
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Update cambia nombre, teléfono y nota del cliente id; by es quién.
// Errores como Create, y service.ErrNotFound si no existe.
func (s *Service) Update(ctx context.Context, by, id int64, c *model.Customer) (*model.Customer, error) {
	if id <= 0 {
		return nil, service.ErrInvalidID
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}
	out := &model.Customer{ID: id, Name: c.Name, Phone: c.Phone, Notes: c.Notes}
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		if err := postgres.NewCustomerStore(tx).Update(ctx, out); err != nil {
			return err
		}
		return audit(ctx, tx, by, "UPDATE", out)
	})
	switch {
	case errors.Is(err, postgres.ErrCustomerNameTaken):
		return nil, ErrNameTaken
	case errors.Is(err, store.ErrNotFound):
		return nil, service.ErrNotFound
	case err != nil:
		return nil, err
	}
	return out, nil
}

// SetActive desactiva o reactiva al cliente id; by es quién. Un inactivo no
// aparece en la caja, pero su deuda sigue. service.ErrNotFound si no
// existe.
func (s *Service) SetActive(ctx context.Context, by, id int64, active bool) error {
	if id <= 0 {
		return service.ErrInvalidID
	}
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		if err := postgres.NewCustomerStore(tx).SetActive(ctx, id, active); err != nil {
			return err
		}
		return audit(ctx, tx, by, "UPDATE", map[string]any{"id": id, "is_active": active})
	})
	if errors.Is(err, store.ErrNotFound) {
		return service.ErrNotFound
	}
	return err
}

func audit(ctx context.Context, tx postgres.DBTX, by int64, action string, data any) error {
	var id int64
	switch v := data.(type) {
	case *model.Customer:
		id = v.ID
	case map[string]any:
		id, _ = v["id"].(int64)
	}
	_, err := postgres.NewAuditLogStore(tx).Create(ctx, postgres.AuditEntry{
		UserID: &by, Table: "customers", Action: action, RecordID: id, NewData: data,
	})
	return err
}
