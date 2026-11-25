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
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/purchases"
)

// fakePurchases imita al caso de uso de compras en memoria. Proveedor 1:
// activo; 2: inactivo; 3: "sin local activo". Producto 1: activo; 2:
// inactivo; cualquier otro no existe. La compra 40 existe.
type fakePurchases struct{}

func (fakePurchases) Receive(_ context.Context, p *model.Purchase) (*purchases.Result, error) {
	if err := p.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", service.ErrValidation, err)
	}
	switch p.SupplierID {
	case 1:
	case 2:
		return nil, purchases.ErrSupplierInactive
	case 3:
		return nil, fmt.Errorf("%w: %w", service.ErrConflict, store.ErrNoActiveStore)
	default:
		return nil, purchases.ErrUnknownSupplier
	}
	for i, it := range p.Items {
		switch it.ProductID {
		case 1:
		case 2:
			return nil, fmt.Errorf("line %d: %w", i+1, purchases.ErrProductInactive)
		default:
			return nil, fmt.Errorf("line %d: %w", i+1, purchases.ErrUnknownProduct)
		}
	}
	p.ID, p.Status = 41, "RECEIVED"
	suggested := decimal.RequireFromString("172.86")
	return &purchases.Result{Purchase: p, TotalDifference: p.TotalDifference(),
		Suggestions: []purchases.Suggestion{{ProductID: 1, ProductName: "OREO", UnitCost: p.Items[0].UnitCost,
			AvgCost: p.Items[0].UnitCost, SuggestedPrice: &suggested}}}, nil
}

func (fakePurchases) Get(_ context.Context, id int64) (*model.Purchase, error) {
	if id <= 0 {
		return nil, service.ErrInvalidID
	}
	if id != 40 {
		return nil, service.ErrNotFound
	}
	return &model.Purchase{ID: 40, SupplierID: 1, Status: "RECEIVED", Items: []*model.PurchaseItem{
		{ID: 1, PurchaseID: 40, ProductID: 1, Quantity: decimal.NewFromInt(10), UnitCost: decimal.NewFromInt(100)}}}, nil
}

func TestPurchasesReceive(t *testing.T) {
	t.Parallel()

	ok := `"items": [{"product_id": 1, "quantity": "10", "unit_cost": "100"}]`
	cases := []struct {
		name     string
		body     string
		want     int
		wantBody string
	}{
		{"received", `{"supplier_id": 1, "voucher_number": "A-1", "total": "1000", ` + ok + `}`,
			http.StatusCreated, `"total_difference":"0"`},
		{"suggestion in the response", `{"supplier_id": 1, "total": "1000", ` + ok + `}`,
			http.StatusCreated, `"suggested_price":"172.86"`},
		{"total difference is a warning", `{"supplier_id": 1, "total": "1200", ` + ok + `}`,
			http.StatusCreated, `"total_difference":"200"`},
		{"batch and expiry", `{"supplier_id": 1, "total": "150", "items": [{"product_id": 1, "quantity": 2.5,
			"unit_cost": 60, "batch_number": "L-1", "expiry_date": "2027-03-31"}]}`,
			http.StatusCreated, `"purchase":{"id":41`},
		{"no lines", `{"supplier_id": 1, "total": "0", "items": []}`,
			http.StatusBadRequest, "at least one line"},
		{"bad line names it", `{"supplier_id": 1, "total": "0", "items": [{"product_id": 1, "quantity": "1",
			"unit_cost": "1"}, {"product_id": 1, "quantity": "0", "unit_cost": "1"}]}`,
			http.StatusBadRequest, "line 2: purchase line quantity"},
		{"unknown supplier", `{"supplier_id": 9, "total": "0", ` + ok + `}`,
			http.StatusBadRequest, "supplier does not exist"},
		{"inactive supplier", `{"supplier_id": 2, "total": "0", ` + ok + `}`,
			http.StatusConflict, "supplier is inactive"},
		{"no active store", `{"supplier_id": 3, "total": "0", ` + ok + `}`,
			http.StatusConflict, "there is no active store"},
		{"inactive product", `{"supplier_id": 1, "total": "0", "items": [{"product_id": 2, "quantity": "1",
			"unit_cost": "1"}]}`, http.StatusConflict, "line 1: product is inactive"},
		{"server fields are rejected", `{"supplier_id": 1, "total": "0", "status": "VOIDED", ` + ok + `}`,
			http.StatusBadRequest, "invalid request body"},
		{"batch id is rejected", `{"supplier_id": 1, "total": "0", "items": [{"product_id": 1, "quantity": "1",
			"unit_cost": "1", "batch_id": 5}]}`, http.StatusBadRequest, "invalid request body"},
		{"not json", `mil`, http.StatusBadRequest, "invalid request body"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/api/purchases", strings.NewReader(tc.body))
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

func TestPurchasesGet(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		path     string
		want     int
		wantBody string
	}{
		{"found", "/api/purchases/40", http.StatusOK, `"items":[{"id":1,"purchase_id":40,"product_id":1`},
		{"missing", "/api/purchases/999", http.StatusNotFound, "purchase not found"},
		{"invalid id", "/api/purchases/abc", http.StatusBadRequest, "invalid purchase id"},
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
