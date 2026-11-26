package postgres_test

import (
	"errors"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// Cada caso apaga todos los locales dentro de la transacción del test (que se
// deshace) y prende los que necesita.
func TestStoreStore_ActiveID(t *testing.T) {
	tests := []struct {
		name    string
		active  int
		wantErr error
	}{
		{name: "none", active: 0, wantErr: store.ErrNoActiveStore},
		{name: "one", active: 1},
		{name: "two", active: 2, wantErr: store.ErrSeveralActiveStores},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newProductFixture(t)
			if _, err := f.tx.ExecContext(f.ctx, `UPDATE stores SET is_active = FALSE;`); err != nil {
				t.Fatal(err)
			}
			var lastID int64
			for i := 0; i < tt.active; i++ {
				if err := f.tx.QueryRowContext(f.ctx,
					`INSERT INTO stores (name) VALUES ('Local test activo') RETURNING id;`).Scan(&lastID); err != nil {
					t.Fatal(err)
				}
			}

			got, err := postgres.NewStoreStore(f.tx).ActiveID(f.ctx)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("got %d, %v, want %v", got, err, tt.wantErr)
				}
				if !errors.Is(err, store.ErrConflict) {
					t.Errorf("error %v is not a conflict", err)
				}
				return
			}
			if err != nil || got != lastID {
				t.Errorf("got %d, %v, want %d", got, err, lastID)
			}
		})
	}
}

func TestStoreStore_Lock(t *testing.T) {
	f := newProductFixture(t)
	stores := postgres.NewStoreStore(f.tx)
	id := f.insertID(t, `INSERT INTO stores (name) VALUES ('Local test lock') RETURNING id;`)
	if err := stores.Lock(f.ctx, id); err != nil {
		t.Errorf("lock: %v", err)
	}
	if err := stores.Lock(f.ctx, -1); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown store: got %v, want %v", err, store.ErrNotFound)
	}
}
