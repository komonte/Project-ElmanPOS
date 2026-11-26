package postgres

import (
	"context"
	"encoding/json"
)

// AuditEntry es una fila de audit_log (02, NFR-03): quién cambió qué tabla,
// qué fila y cómo quedó. NewData se guarda como JSON.
type AuditEntry struct {
	// UserID es quién lo hizo (F-13). nil si no fue alguien que entró: el
	// comando passwd, o lo de antes del login.
	UserID   *int64
	Table    string
	Action   string // INSERT, UPDATE, DELETE
	RecordID int64
	NewData  any
}

// AuditLogStore escribe el registro de auditoría. Solo agrega: audit_log no se
// edita ni se borra.
type AuditLogStore struct {
	db DBTX
}

// NewAuditLogStore recibe un DBTX: casi siempre una transacción, así el
// cambio y su registro se guardan juntos (o ninguno).
func NewAuditLogStore(db DBTX) *AuditLogStore {
	return &AuditLogStore{db: db}
}

// Create guarda una entrada y devuelve su id.
func (s *AuditLogStore) Create(ctx context.Context, e AuditEntry) (int64, error) {
	newData, err := json.Marshal(e.NewData)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO audit_log (user_id, table_name, action, record_id, new_data)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id;`,
		e.UserID, e.Table, e.Action, e.RecordID, newData).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, nil
}
