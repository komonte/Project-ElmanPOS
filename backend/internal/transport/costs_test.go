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
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/costs"
)

// fakeCosts imita al caso de uso de costos en memoria. El producto 1 está
// activo, con margen 30% y un costo; el 2 está desactivado; el 3 está activo
// con margen 0; cualquier otro no existe. El promedio no se calcula acá (eso
// lo prueba usecase/costs): se copia el costo.
type fakeCosts struct {
	history map[int64][]*model.Cost
	nextID  int64
}

func newFakeCosts() *fakeCosts {
	return &fakeCosts{
		history: map[int64][]*model.Cost{
			1: {{ID: 30, ProductID: 1, UnitCost: decimal.RequireFromString("500"),
				AvgCost: decimal.RequireFromString("480"), ValidFrom: "2026-11-01T10:00:00Z"}},
			2: {},
			3: {},
		},
		nextID: 31,
	}
}

func (f *fakeCosts) Set(_ context.Context, productID int64, unitCost decimal.Decimal) (*costs.Result, error) {
	if productID <= 0 {
		return nil, service.ErrInvalidID
	}
	c := &model.Cost{ProductID: productID, UnitCost: unitCost, AvgCost: unitCost}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}
	margin := "0"
	switch productID {
	case 1:
		margin = "30"
	case 2:
		return nil, costs.ErrProductInactive
	case 3:
	default:
		return nil, service.ErrNotFound
	}
	c.ID = f.nextID
	f.nextID++
	c.ValidFrom = "2026-11-25T10:00:00Z"
	f.history[productID] = append([]*model.Cost{c}, f.history[productID]...)

	res := &costs.Result{Cost: c}
	if price, ok := model.SuggestPrice(unitCost, decimal.RequireFromString(margin)); ok {
		res.SuggestedPrice = &price
	}
	return res, nil
}

func (f *fakeCosts) History(_ context.Context, productID int64) ([]*model.Cost, error) {
	h, ok := f.history[productID]
	if !ok {
		return nil, service.ErrNotFound
	}
	return h, nil
}

func TestCostsSet(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		path     string
		body     string
		want     int
		wantBody string // texto que tiene que aparecer en la respuesta
	}{
		{"cost as text, with suggestion", "/api/products/1/costs", `{"unit_cost": "700"}`,
			http.StatusCreated, `"unit_cost":"700","avg_cost":"700"`},
		{"suggested price", "/api/products/1/costs", `{"unit_cost": "700"}`,
			http.StatusCreated, `"suggested_price":"1000"`},
		{"cost as number", "/api/products/1/costs", `{"unit_cost": 700.50}`,
			http.StatusCreated, `"unit_cost":"700.5"`},
		{"margin 0: no suggestion", "/api/products/3/costs", `{"unit_cost": "700"}`,
			http.StatusCreated, `"suggested_price":null`},
		{"zero", "/api/products/1/costs", `{"unit_cost": "0"}`,
			http.StatusBadRequest, "cost must be greater than 0"},
		{"missing cost", "/api/products/1/costs", `{}`,
			http.StatusBadRequest, "cost must be greater than 0"},
		{"three decimals", "/api/products/1/costs", `{"unit_cost": "10.555"}`,
			http.StatusBadRequest, "up to 2 decimals"},
		{"not a number", "/api/products/1/costs", `{"unit_cost": "mil"}`,
			http.StatusBadRequest, "invalid request body"},
		{"average is not accepted from the client", "/api/products/1/costs",
			`{"unit_cost": "700", "avg_cost": "1"}`,
			http.StatusBadRequest, "invalid request body"},
		{"inactive product", "/api/products/2/costs", `{"unit_cost": "100"}`,
			http.StatusConflict, "product is inactive"},
		{"missing product", "/api/products/999/costs", `{"unit_cost": "100"}`,
			http.StatusNotFound, "product not found"},
		{"invalid id", "/api/products/abc/costs", `{"unit_cost": "100"}`,
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

func TestCostsList(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		path     string
		want     int
		wantBody string
	}{
		{"with a cost", "/api/products/1/costs", http.StatusOK,
			`"current":{"id":30,"product_id":1,"unit_cost":"500","avg_cost":"480"`},
		{"without costs", "/api/products/2/costs", http.StatusOK,
			`{"current":null,"history":[]}`},
		{"missing product", "/api/products/999/costs", http.StatusNotFound,
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
