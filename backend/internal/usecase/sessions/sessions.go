// Package sessions es el caso de uso F-11: abrir la caja con el efectivo
// inicial y saber si está abierta. El cierre con arqueo (F-12) llega después.
//
// Decisiones (2026-11-26):
//   - Se usa la única caja activa del único local activo. Si el local no
//     tiene cajas (una base con el catálogo real), la pantalla ofrece crear
//     "Caja 1" (CreateRegister). Con más de una caja activa no se adivina.
//   - Una sola sesión abierta por caja (BR-02, índice de la 0017).
//   - El efectivo inicial puede ser 0.
//   - Sin login todavía: opened_by queda NULL.
//
// Los errores salen como los del service genérico (service.ErrValidation,
// service.ErrConflict...), así la API y la web los traducen igual.
package sessions

import (
	"context"
	"errors"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// DefaultRegisterName es el nombre de la caja que crea CreateRegister.
const DefaultRegisterName = "Caja 1"

var ErrNoRegister = fmt.Errorf("the store has no cash register: %w", service.ErrConflict)

type Service struct {
	db postgres.DBTX     // lecturas y la apertura (un solo INSERT)
	tx postgres.TxRunner // CreateRegister
}

func New(db postgres.DBTX, tx postgres.TxRunner) *Service {
	return &Service{db: db, tx: tx}
}

// State es cómo está la caja: cuál es y su sesión abierta, si tiene.
type State struct {
	// Register es nil si el local no tiene cajas.
	Register *model.CashRegister `json:"register"`
	// Session es nil si la caja está cerrada (o no hay caja).
	Session *model.CashierSession `json:"session"`
}

// Current devuelve la caja del local activo y su sesión abierta.
//   - service.ErrConflict: no hay un único local activo, o el local tiene
//     más de una caja activa.
func (s *Service) Current(ctx context.Context) (*State, error) {
	reg, err := register(ctx, s.db)
	if errors.Is(err, ErrNoRegister) {
		return &State{}, nil
	}
	if err != nil {
		return nil, err
	}
	st := &State{Register: reg}
	sess, err := postgres.NewCashierSessionStore(s.db).OpenFor(ctx, reg.ID)
	switch {
	case err == nil:
		st.Session = sess
	case !errors.Is(err, store.ErrNotFound):
		return nil, err
	}
	return st, nil
}

// CreateRegister crea "Caja 1" en el local activo si no tiene ninguna caja
// activa, y la devuelve. Si ya tiene una, devuelve esa: dos clics seguidos
// no crean dos cajas (el local queda trabado mientras tanto).
//   - service.ErrConflict: no hay un único local activo, o ya hay más de una
//     caja activa.
func (s *Service) CreateRegister(ctx context.Context) (*model.CashRegister, error) {
	var reg *model.CashRegister
	err := s.tx.InTx(ctx, func(tx postgres.DBTX) error {
		storeID, err := activeStore(ctx, tx)
		if err != nil {
			return err
		}
		if err := postgres.NewStoreStore(tx).Lock(ctx, storeID); err != nil {
			return err
		}
		registers := postgres.NewCashRegisterStore(tx)
		reg, err = registers.Only(ctx, storeID)
		if errors.Is(err, store.ErrNotFound) {
			reg, err = registers.Create(ctx, storeID, DefaultRegisterName)
		}
		return err
	})
	if err != nil {
		return nil, translate(err)
	}
	return reg, nil
}

// Open abre la caja del local activo con el efectivo inicial.
//   - service.ErrValidation: el monto está mal (envuelve el error del model).
//   - service.ErrConflict: no hay caja (ErrNoRegister), ya está abierta
//     (store.ErrSessionAlreadyOpen adentro), o no hay un único local o caja.
func (s *Service) Open(ctx context.Context, openingAmount decimal.Decimal) (*model.CashierSession, error) {
	reg, err := register(ctx, s.db)
	if err != nil {
		return nil, err
	}
	in := &model.CashierSession{CashRegisterID: reg.ID, OpeningAmount: openingAmount}
	if err := in.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}
	sess, err := postgres.NewCashierSessionStore(s.db).Open(ctx, in)
	if err != nil {
		return nil, translate(err)
	}
	return sess, nil
}

// register devuelve la única caja activa del local activo, o ErrNoRegister.
func register(ctx context.Context, db postgres.DBTX) (*model.CashRegister, error) {
	storeID, err := activeStore(ctx, db)
	if err != nil {
		return nil, err
	}
	reg, err := postgres.NewCashRegisterStore(db).Only(ctx, storeID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrNoRegister
	}
	if err != nil {
		return nil, translate(err)
	}
	return reg, nil
}

func activeStore(ctx context.Context, db postgres.DBTX) (int64, error) {
	id, err := postgres.NewStoreStore(db).ActiveID(ctx)
	if err != nil {
		return 0, translate(err)
	}
	return id, nil
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
