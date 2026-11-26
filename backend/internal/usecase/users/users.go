// Package users es la página de usuarios de F-13: el admin crea usuarios,
// les cambia el nombre y el rol, y los desactiva o reactiva. La contraseña
// se resetea con auth.SetPassword (también saca la traba por fallos, que
// vive en auth.Service).
//
// Decisiones (2026-11-26):
//   - No se borra un usuario: ventas, movimientos y auditoría lo nombran. Se
//     desactiva, y eso cierra sus sesiones en el momento.
//   - El usuario con que se entra no cambia después de crearlo. Si se
//     escribió mal, se desactiva y se crea otro.
//   - Un admin no puede desactivarse ni cambiarse el rol a sí mismo
//     (ErrSelfDeactivate, ErrSelfRole): se quedaría afuera sin querer.
//   - Siempre queda al menos un admin activo (ErrLastAdmin). La regla de "a
//     sí mismo" no alcanza: si dos admins se sacan el rol uno al otro a la
//     vez, cada uno ve al otro como "el que queda". Por eso, antes de decidir,
//     se bloquean los admins activos (UserStore.LockActiveAdmins) y el usuario
//     se lee después del bloqueo: la segunda transacción espera a la primera
//     y ve lo que hizo.
//   - Cambiar el rol no cierra sesiones: el rol se lee de la base en cada
//     pedido, así que vale desde el próximo clic.
//   - by es el admin que entró: va al user_id de cada fila de audit_log.
//
// Los errores salen como los del service genérico (service.ErrValidation,
// service.ErrConflict...), así la web los traduce igual.
package users

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

var (
	ErrSelfDeactivate = fmt.Errorf("you cannot deactivate yourself: %w", service.ErrConflict)
	ErrSelfRole       = fmt.Errorf("you cannot change your own role: %w", service.ErrConflict)
	ErrLastAdmin      = fmt.Errorf("there must be at least one active administrator: %w", service.ErrConflict)
)

type Service struct {
	db postgres.DBTX     // lecturas
	tx postgres.TxRunner // cada cambio, con su auditoría
}

func New(db postgres.DBTX, tx postgres.TxRunner) *Service {
	return &Service{db: db, tx: tx}
}

// List devuelve todos los usuarios: primero los activos, por nombre.
func (s *Service) List(ctx context.Context) ([]*model.User, error) {
	return postgres.NewUserStore(s.db).List(ctx)
}

// Get devuelve un usuario, activo o no. service.ErrNotFound si no existe.
func (s *Service) Get(ctx context.Context, id int64) (*model.User, error) {
	u, err := postgres.NewUserStore(s.db).Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, service.ErrNotFound
	}
	return u, err
}

// Create da de alta un usuario activo con su contraseña inicial.
//   - service.ErrValidation: usuario, nombre, rol o contraseña inválidos.
//   - service.ErrAlreadyExists (y store.ErrDuplicateUsername): el usuario
//     ya existe, sin importar mayúsculas.
func (s *Service) Create(ctx context.Context, by int64, u *model.User, password string) (*model.User, error) {
	if err := u.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}
	hash, err := model.HashPassword(password)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}
	u.PasswordHash, u.IsActive = hash, true
	err = s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		if err := postgres.NewUserStore(tx).Create(ctx, u); err != nil {
			return err
		}
		return audit(ctx, tx, by, "INSERT", u.ID, map[string]any{
			"username": u.Username, "name": u.Name, "role": u.Role,
		})
	})
	if errors.Is(err, store.ErrDuplicate) {
		return nil, fmt.Errorf("%w: %w", service.ErrAlreadyExists, err)
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// Update cambia el nombre y el rol. Si no cambia nada, no escribe.
//   - service.ErrValidation: nombre o rol inválidos.
//   - service.ErrNotFound: el usuario no existe.
//   - ErrSelfRole: es el mismo admin y cambia su rol (el nombre sí puede).
//   - ErrLastAdmin: le saca el rol al último admin activo.
func (s *Service) Update(ctx context.Context, by, id int64, name, role string) (*model.User, error) {
	var u *model.User
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		users := postgres.NewUserStore(tx)
		admins, err := users.LockActiveAdmins(ctx)
		if err != nil {
			return err
		}
		old, err := users.Get(ctx, id)
		if err != nil {
			return err
		}
		next := *old
		next.Name, next.Role = name, role
		if err := next.Validate(); err != nil {
			return fmt.Errorf("%w: %w", service.ErrValidation, err)
		}
		u = &next
		if next.Name == old.Name && next.Role == old.Role {
			return nil
		}
		if next.Role != old.Role {
			if id == by {
				return ErrSelfRole
			}
			if lastAdmin(admins, id) {
				return ErrLastAdmin
			}
		}
		if err := users.Update(ctx, id, next.Name, next.Role); err != nil {
			return err
		}
		return audit(ctx, tx, by, "UPDATE", id, map[string]any{
			"before": map[string]any{"name": old.Name, "role": old.Role},
			"after":  map[string]any{"name": next.Name, "role": next.Role},
		})
	})
	if errors.Is(err, store.ErrNotFound) {
		return nil, service.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return u, nil
}

// Deactivate desactiva un usuario y cierra todas sus sesiones: deja de
// entrar en el próximo clic. Si ya estaba inactivo, no hace nada.
//   - service.ErrNotFound: el usuario no existe.
//   - ErrSelfDeactivate: es el mismo admin.
//   - ErrLastAdmin: es el último admin activo.
func (s *Service) Deactivate(ctx context.Context, by, id int64) error {
	if id == by {
		return ErrSelfDeactivate
	}
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		users := postgres.NewUserStore(tx)
		admins, err := users.LockActiveAdmins(ctx)
		if err != nil {
			return err
		}
		u, err := users.Get(ctx, id)
		if err != nil {
			return err
		}
		if !u.IsActive {
			return nil
		}
		if lastAdmin(admins, id) {
			return ErrLastAdmin
		}
		if err := users.SetActive(ctx, id, false); err != nil {
			return err
		}
		closed, err := postgres.NewUserSessionStore(tx).DeleteForUser(ctx, id)
		if err != nil {
			return err
		}
		return audit(ctx, tx, by, "DEACTIVATE", id, map[string]any{
			"username": u.Username, "sessions_closed": closed,
		})
	})
	if errors.Is(err, store.ErrNotFound) {
		return service.ErrNotFound
	}
	return err
}

// Reactivate vuelve a activar un usuario, con la contraseña que tenía. Si
// ya estaba activo, no hace nada. service.ErrNotFound si no existe.
func (s *Service) Reactivate(ctx context.Context, by, id int64) error {
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		users := postgres.NewUserStore(tx)
		u, err := users.Get(ctx, id)
		if err != nil {
			return err
		}
		if u.IsActive {
			return nil
		}
		if err := users.SetActive(ctx, id, true); err != nil {
			return err
		}
		return audit(ctx, tx, by, "REACTIVATE", id, map[string]any{"username": u.Username})
	})
	if errors.Is(err, store.ErrNotFound) {
		return service.ErrNotFound
	}
	return err
}

// lastAdmin dice si id es el único admin activo (de los bloqueados): sacarlo
// dejaría el sistema sin ninguno. Si id no es un admin activo, no lo es.
func lastAdmin(admins []int64, id int64) bool {
	return len(admins) == 1 && slices.Contains(admins, id)
}

func audit(ctx context.Context, tx postgres.DBTX, by int64, action string, id int64, data map[string]any) error {
	_, err := postgres.NewAuditLogStore(tx).Create(ctx, postgres.AuditEntry{
		UserID: &by, Table: "users", Action: action, RecordID: id, NewData: data,
	})
	return err
}
