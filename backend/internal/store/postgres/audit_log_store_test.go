package postgres_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

func TestAuditLogStore_Create(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	audit := postgres.NewAuditLogStore(tx)

	id, err := audit.Create(ctx, postgres.AuditEntry{
		Table:    "prices",
		Action:   "INSERT",
		RecordID: 42,
		NewData:  map[string]any{"unit_price": "1500.50"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var (
		userID        *int64
		table, action string
		recordID      int64
		newData       []byte
	)
	err = tx.QueryRowContext(ctx,
		`SELECT user_id, table_name, action, record_id, new_data FROM audit_log WHERE id = $1;`, id).
		Scan(&userID, &table, &action, &recordID, &newData)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if userID != nil || table != "prices" || action != "INSERT" || recordID != 42 {
		t.Errorf("got user %v, table %q, action %q, record %d", userID, table, action, recordID)
	}
	var data map[string]any
	if err := json.Unmarshal(newData, &data); err != nil || data["unit_price"] != "1500.50" {
		t.Errorf("new_data: got %s (err %v)", newData, err)
	}
}
