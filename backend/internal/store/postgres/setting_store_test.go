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

func TestSettingStore_Bool(t *testing.T) {
	f := newProductFixture(t)
	settings := postgres.NewSettingStore(f.tx)

	// La 0021 carga el modo arranque, apagado.
	if got, err := settings.Bool(f.ctx, "startup_mode"); err != nil || got {
		t.Fatalf("startup_mode: got %v, %v; want false", got, err)
	}
	if err := settings.SetBool(f.ctx, "startup_mode", true); err != nil {
		t.Fatal(err)
	}
	if got, err := settings.Bool(f.ctx, "startup_mode"); err != nil || !got {
		t.Errorf("after SetBool(true): got %v, %v", got, err)
	}
	if _, err := settings.Bool(f.ctx, "max_sale_weight_kg"); err == nil {
		t.Error("number setting read as bool: got no error")
	}
	if _, err := settings.Bool(f.ctx, "test_missing"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing: got %v, want %v", err, store.ErrNotFound)
	}
	if err := settings.SetBool(f.ctx, "test_missing", true); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("SetBool missing: got %v, want %v", err, store.ErrNotFound)
	}
}
