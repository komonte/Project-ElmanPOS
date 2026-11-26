package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// CashRegisterStore lee y crea las cajas de un local (02 4.9).
type CashRegisterStore struct {
	db DBTX
}

func NewCashRegisterStore(db DBTX) *CashRegisterStore {
	return &CashRegisterStore{db: db}
}

// Only devuelve la única caja activa de un local: ahí se abre la sesión y se
// vende mientras haya una sola.
//   - ErrNotFound: el local no tiene cajas activas (una base con el
//     catálogo real arranca así).
//   - ErrSeveralActiveRegisters: tiene más de una. No se elige una al azar:
//     el día que haya dos, la pantalla va a tener que preguntar cuál.
func (s *CashRegisterStore) Only(ctx context.Context, storeID int64) (*model.CashRegister, error) {
	// LIMIT 2: alcanza para saber si hay una sola.
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, store_id, name, is_active FROM cash_registers
		WHERE store_id = $1 AND is_active
		ORDER BY id LIMIT 2;`, storeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []*model.CashRegister
	for rows.Next() {
		r := new(model.CashRegister)
		if err := rows.Scan(&r.ID, &r.StoreID, &r.Name, &r.IsActive); err != nil {
			return nil, err
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	switch len(list) {
	case 0:
		return nil, store.ErrNotFound
	case 1:
		return list[0], nil
	default:
		return nil, store.ErrSeveralActiveRegisters
	}
}

// Create guarda una caja activa en un local (el nombre, recortado).
//   - ErrInvalidReference: el local no existe.
func (s *CashRegisterStore) Create(ctx context.Context, storeID int64, name string) (*model.CashRegister, error) {
	r := &model.CashRegister{StoreID: storeID, Name: strings.TrimSpace(name), IsActive: true}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO cash_registers (store_id, name) VALUES ($1, $2) RETURNING id;`,
		r.StoreID, r.Name).Scan(&r.ID)
	if err != nil {
		if isForeignKeyViolation(err) {
			return nil, store.ErrInvalidReference
		}
		return nil, err
	}
	return r, nil
}

// CashierSessionStore guarda y lee las sesiones de caja (02 4.15, F-11).
type CashierSessionStore struct {
	db DBTX
}

func NewCashierSessionStore(db DBTX) *CashierSessionStore {
	return &CashierSessionStore{db: db}
}

const sessionColumns = `cs.id, cs.cash_register_id, cs.opened_by, cs.opening_amount, cs.closing_amount,
	cs.status, cs.opened_at, cs.closed_at, r.name`

func scanSession(row rowScanner) (*model.CashierSession, error) {
	s := new(model.CashierSession)
	err := row.Scan(&s.ID, &s.CashRegisterID, &s.OpenedBy, &s.OpeningAmount, &s.ClosingAmount,
		&s.Status, &s.OpenedAt, &s.ClosedAt, &s.RegisterName)
	return s, err
}

// Open abre una sesión en la caja con el efectivo inicial y la devuelve.
//   - ErrSessionAlreadyOpen: la caja ya tiene una abierta (índice único de
//     la 0017: vale también si dos pantallas abren a la vez).
//   - ErrInvalidReference: la caja no existe.
func (s *CashierSessionStore) Open(ctx context.Context, in *model.CashierSession) (*model.CashierSession, error) {
	if in == nil {
		return nil, model.ErrNilCashierSession
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO cashier_sessions (cash_register_id, opened_by, opening_amount)
		VALUES ($1, $2, $3) RETURNING id;`,
		in.CashRegisterID, in.OpenedBy, in.OpeningAmount).Scan(&id)
	if err != nil {
		switch {
		case isUniqueViolation(err):
			return nil, store.ErrSessionAlreadyOpen
		case isForeignKeyViolation(err):
			return nil, store.ErrInvalidReference
		}
		return nil, err
	}
	return s.get(ctx, `cs.id = $1`, id)
}

// OpenFor devuelve la sesión abierta de una caja. ErrNotFound si no tiene.
func (s *CashierSessionStore) OpenFor(ctx context.Context, registerID int64) (*model.CashierSession, error) {
	return s.get(ctx, `cs.cash_register_id = $1 AND cs.status = 'OPEN'`, registerID)
}

// LockOpen traba la sesión hasta que termine la transacción, si sigue
// abierta (F-07 paso 2, BR-02). Mientras tanto otra venta de la misma caja
// espera, y un cierre también: el número de venta siguiente no se repite y
// no se vende en una sesión que se está cerrando.
//   - ErrSessionNotOpen: no existe o ya se cerró.
func (s *CashierSessionStore) LockOpen(ctx context.Context, id int64) (*model.CashierSession, error) {
	got, err := s.get(ctx, `cs.id = $1 AND cs.status = 'OPEN' FOR UPDATE OF cs`, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, store.ErrSessionNotOpen
	}
	return got, err
}

// Close cierra una sesión abierta (F-12): estado CLOSED, lo declarado en
// efectivo y la hora. Quien cierra la trabó antes (LockOpen), así que ninguna
// venta entra mientras tanto. Devuelve la sesión cerrada.
//   - ErrSessionNotOpen: no existe o ya estaba cerrada.
func (s *CashierSessionStore) Close(ctx context.Context, id int64, closingAmount decimal.Decimal) (*model.CashierSession, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE cashier_sessions
		SET status = 'CLOSED', closing_amount = $2, closed_at = now()
		WHERE id = $1 AND status = 'OPEN';`, id, closingAmount)
	if err != nil {
		return nil, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return nil, err
	} else if n == 0 {
		return nil, store.ErrSessionNotOpen
	}
	return s.get(ctx, `cs.id = $1`, id)
}

// Get devuelve una sesión, abierta o cerrada (el resumen del cierre).
// ErrNotFound si no existe.
func (s *CashierSessionStore) Get(ctx context.Context, id int64) (*model.CashierSession, error) {
	return s.get(ctx, `cs.id = $1`, id)
}

func (s *CashierSessionStore) get(ctx context.Context, where string, arg any) (*model.CashierSession, error) {
	got, err := scanSession(s.db.QueryRowContext(ctx, `
		SELECT `+sessionColumns+`
		FROM cashier_sessions cs
		JOIN cash_registers r ON r.id = cs.cash_register_id
		WHERE `+where+`;`, arg))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	return got, nil
}
