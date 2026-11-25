package transport_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/prices"
)

// fakePrices imita al caso de uso de precios en memoria. El producto 1 (OREO,
// ver newTestMux) está activo y ya tiene un precio; el 2 está desactivado;
// cualquier otro no existe.
type fakePrices struct {
	history map[int64][]*model.Price
	nextID  int64
}

func newFakePrices() *fakePrices {
	return &fakePrices{
		history: map[int64][]*model.Price{
			1: {{ID: 20, ProductID: 1, UnitPrice: decimal.RequireFromString("1200"), ValidFrom: "2026-11-01T10:00:00Z"}},
			2: {},
		},
		nextID: 21,
	}
}

func (f *fakePrices) Set(_ context.Context, productID int64, unitPrice decimal.Decimal) (*model.Price, error) {
	if productID <= 0 {
		return nil, service.ErrInvalidID
	}
	p := &model.Price{ProductID: productID, UnitPrice: unitPrice}
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}
	switch productID {
	case 1:
	case 2:
		return nil, prices.ErrProductInactive
	default:
		return nil, service.ErrNotFound
	}
	p.ID = f.nextID
	f.nextID++
	p.ValidFrom = "2026-11-25T10:00:00Z"
	f.history[productID] = append([]*model.Price{p}, f.history[productID]...)
	return p, nil
}

func (f *fakePrices) History(_ context.Context, productID int64) ([]*model.Price, error) {
	h, ok := f.history[productID]
	if !ok {
		return nil, service.ErrNotFound
	}
	return h, nil
}

func TestPricesSet(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		path     string
		body     string
		want     int
		wantBody string // texto que tiene que aparecer en la respuesta
	}{
		{"price as text", "/api/products/1/prices", `{"unit_price": "1500.50"}`,
			http.StatusCreated, `"unit_price":"1500.5"`},
		{"price as number", "/api/products/1/prices", `{"unit_price": 1500.50}`,
			http.StatusCreated, `"unit_price":"1500.5"`},
		{"zero", "/api/products/1/prices", `{"unit_price": "0"}`,
			http.StatusBadRequest, "price must be greater than 0"},
		{"missing price", "/api/products/1/prices", `{}`,
			http.StatusBadRequest, "price must be greater than 0"},
		{"three decimals", "/api/products/1/prices", `{"unit_price": "10.555"}`,
			http.StatusBadRequest, "up to 2 decimals"},
		{"not a number", "/api/products/1/prices", `{"unit_price": "mil"}`,
			http.StatusBadRequest, "invalid request body"},
		{"date is not accepted from the client", "/api/products/1/prices",
			`{"unit_price": "100", "valid_from": "2027-01-01"}`,
			http.StatusBadRequest, "invalid request body"},
		{"inactive product", "/api/products/2/prices", `{"unit_price": "100"}`,
			http.StatusConflict, "product is inactive"},
		{"missing product", "/api/products/999/prices", `{"unit_price": "100"}`,
			http.StatusNotFound, "product not found"},
		{"invalid id", "/api/products/abc/prices", `{"unit_price": "100"}`,
			http.StatusBadRequest, "invalid product id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
			rec := httptest.NewRecorder()
			newTestMux().ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status: got %d, want %d. Body: %s", rec.Code, tc.want, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body %q does not contain %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}

func TestPricesList(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		path     string
		want     int
		wantBody string
	}{
		{"with a price", "/api/products/1/prices", http.StatusOK,
			`"current":{"id":20,"product_id":1,"unit_price":"1200"`},
		{"without prices", "/api/products/2/prices", http.StatusOK,
			`{"current":null,"history":[]}`},
		{"missing product", "/api/products/999/prices", http.StatusNotFound,
			"product not found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			newTestMux().ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("status: got %d, want %d. Body: %s", rec.Code, tc.want, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("body %q does not contain %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}
