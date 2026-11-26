package postgres

import (
	"context"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// CashAuditStore guarda y lee lo declarado al cerrar la caja (02 4.20,
// F-12): un monto por medio de pago y sesión.
type CashAuditStore struct {
	db DBTX
}

func NewCashAuditStore(db DBTX) *CashAuditStore {
	return &CashAuditStore{db: db}
}

// Create guarda un monto declarado y llena su id y fecha.
//   - ErrDuplicate: ese medio ya se declaró en la sesión (índice de la 0018).
//   - ErrInvalidReference: la sesión no existe.
func (s *CashAuditStore) Create(ctx context.Context, a *model.CashAudit) error {
	if a == nil {
		return model.ErrNilCashAudit
	}
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO cash_audits (cashier_session_id, method, declared_amount)
		VALUES ($1, $2, $3) RETURNING id, created_at;`,
		a.CashierSessionID, a.Method, a.DeclaredAmount).Scan(&a.ID, &a.CreatedAt)
	if err != nil {
		switch {
		case isUniqueViolation(err):
			return store.ErrDuplicate
		case isForeignKeyViolation(err):
			return store.ErrInvalidReference
		}
		return err
	}
	return nil
}

// ListBySession devuelve lo declarado en una sesión, en el orden en que se
// guardó. Vacía, no nil, si la sesión no se cerró.
func (s *CashAuditStore) ListBySession(ctx context.Context, sessionID int64) ([]*model.CashAudit, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, cashier_session_id, method, declared_amount, created_at
		FROM cash_audits WHERE cashier_session_id = $1 ORDER BY id;`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := make([]*model.CashAudit, 0)
	for rows.Next() {
		a := new(model.CashAudit)
		if err := rows.Scan(&a.ID, &a.CashierSessionID, &a.Method, &a.DeclaredAmount, &a.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}
