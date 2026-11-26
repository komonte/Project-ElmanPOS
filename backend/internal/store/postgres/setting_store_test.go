package postgres_test

import (
	"errors"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

func TestSettingStore_Decimal(t *testing.T) {
	f := newProductFixture(t)
	settings := postgres.NewSettingStore(f.tx)
	if _, err := f.tx.ExecContext(f.ctx, `
		INSERT INTO app_settings (key, value) VALUES ('test_number', '2.5'), ('test_text', '"x"');`); err != nil {
		t.Fatal(err)
	}

	if got, err := settings.Decimal(f.ctx, "test_number"); err != nil || !got.Equal(d("2.5")) {
		t.Errorf("number: got %s, %v", got, err)
	}
	// La 0017 carga el peso máximo.
	if got, err := settings.Decimal(f.ctx, "max_sale_weight_kg"); err != nil || !got.Equal(d("50")) {
		t.Errorf("max_sale_weight_kg: got %s, %v", got, err)
	}
	if _, err := settings.Decimal(f.ctx, "test_text"); err == nil {
		t.Error("text setting: got no error")
	}
	if _, err := settings.Decimal(f.ctx, "test_missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing: got %v, want %v", err, store.ErrNotFound)
	}
}
