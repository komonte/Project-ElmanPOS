package prices

import (
	"errors"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
)

func TestSetOpenPrice(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.SetOpenPrice(f.ctx, f.userID, f.productID, true); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT count(*) FROM products WHERE id = $1 AND open_price;`, f.productID); n != 1 {
		t.Error("not marked")
	}
	if n := f.count(t, `SELECT count(*) FROM audit_log WHERE table_name = 'products' AND record_id = $1
		AND user_id = $2 AND new_data->>'open_price' = 'true';`, f.productID, f.userID); n != 1 {
		t.Errorf("audit rows: %d", n)
	}
	if err := f.svc.SetOpenPrice(f.ctx, f.userID, f.productID, false); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT count(*) FROM products WHERE id = $1 AND open_price;`, f.productID); n != 0 {
		t.Error("still marked")
	}

	var weight, batches int64
	f.scan(t, &weight, `INSERT INTO products (name, category_id, sale_type, measure_unit) VALUES ('PESO TEST', $1, 'WEIGHT', 'KILOGRAM') RETURNING id;`, f.categoryID)
	f.scan(t, &batches, `INSERT INTO products (name, category_id, controls_batches) VALUES ('LOTES TEST', $1, TRUE) RETURNING id;`, f.categoryID)
	for _, id := range []int64{weight, batches} {
		if err := f.svc.SetOpenPrice(f.ctx, f.userID, id, true); !errors.Is(err, ErrOpenPriceKind) {
			t.Errorf("product %d: got %v, want %v", id, err, ErrOpenPriceKind)
		}
	}
	inactive := f.newProduct(t, "INACTIVO TEST", false)
	if err := f.svc.SetOpenPrice(f.ctx, f.userID, inactive, true); !errors.Is(err, ErrProductInactive) {
		t.Errorf("inactive: got %v", err)
	}
	if err := f.svc.SetOpenPrice(f.ctx, f.userID, 999999999, true); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("missing: got %v", err)
	}
}
