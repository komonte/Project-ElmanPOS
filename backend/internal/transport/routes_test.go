package transport_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/productcodes"
	"github.com/shopspring/decimal"
)

// newTestMux builds the same API routing as main.go, backed by mock stores.
func newTestMux() *http.ServeMux {
	brand := &model.Brand{ID: 1, Name: "Sony"}
	tax := &model.Tax{ID: 1, Name: "IVA", Rate: decimal.NewFromInt(21)}

	brandStore := &MockStore[model.Brand]{
		getByIDFn: func(_ context.Context, id int64) (*model.Brand, error) {
			if id != 1 {
				return nil, store.ErrNotFound
			}
			return brand, nil
		},
		deleteFn: func(_ context.Context, id int64) error {
			if id != 1 {
				return store.ErrNotFound
			}
			return nil
		},
	}
	taxStore := &MockStore[model.Tax]{
		getByIDFn: func(_ context.Context, id int64) (*model.Tax, error) {
			if id != 1 {
				return nil, store.ErrNotFound
			}
			return tax, nil
		},
		deleteFn: func(_ context.Context, id int64) error {
			if id != 1 {
				return store.ErrNotFound
			}
			return nil
		},
	}

	supplier := &model.Supplier{ID: 1, Name: "Arcor"}
	supplierStore := &MockStore[model.Supplier]{
		getByIDFn: func(_ context.Context, id int64) (*model.Supplier, error) {
			if id != 1 {
				return nil, store.ErrNotFound
			}
			return supplier, nil
		},
		deleteFn: func(_ context.Context, id int64) error {
			if id != 1 {
				return store.ErrNotFound
			}
			return nil
		},
	}

	category := &model.Category{ID: 1, Name: "Limpieza", TaxID: 1}
	categoryStore := &MockStore[model.Category]{
		getByIDFn: func(_ context.Context, id int64) (*model.Category, error) {
			if id != 1 {
				return nil, store.ErrNotFound
			}
			return category, nil
		},
		deleteFn: func(_ context.Context, id int64) error {
			if id != 1 {
				return store.ErrNotFound
			}
			return nil
		},
	}

	product := &model.Product{ID: 1, SKU: "OREO-200", Name: "GALLETITAS OREO 200G", CategoryID: 1}
	productStore := &MockStore[model.Product]{
		getByIDFn: func(_ context.Context, id int64) (*model.Product, error) {
			if id != 1 {
				return nil, store.ErrNotFound
			}
			return product, nil
		},
		deleteFn: func(_ context.Context, id int64) error {
			if id != 1 {
				return store.ErrNotFound
			}
			return nil
		},
	}

	mux := http.NewServeMux()
	transport.RegisterAPIRoutes(mux,
		transport.NewResourceHandler[model.Brand](service.NewService[model.Brand](brandStore, nil), "brand"),
		transport.NewResourceHandler[model.Tax](service.NewService[model.Tax](taxStore, nil), "tax"),
		transport.NewResourceHandler[model.Supplier](service.NewService[model.Supplier](supplierStore, nil), "supplier"),
		transport.NewResourceHandler[model.Category](service.NewService[model.Category](categoryStore, nil), "category"),
		transport.NewResourceHandler[model.Product](service.NewService[model.Product](productStore, nil), "product"),
		transport.NewProductCodesHandler(productcodes.New(productStore, newFakeCodes())),
		transport.NewPricesHandler(newFakePrices()),
		transport.NewCostsHandler(newFakeCosts()),
		transport.NewPurchasesHandler(fakePurchases{}),
	)
	return mux
}

func TestAPIRoutesThroughMux(t *testing.T) {
	t.Parallel()
	mux := newTestMux()

	cases := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodGet, "/api/brands/1", http.StatusOK},
		{http.MethodGet, "/api/brands/999", http.StatusNotFound},
		{http.MethodGet, "/api/brands/abc", http.StatusBadRequest},
		{http.MethodDelete, "/api/brands/1", http.StatusNoContent},
		{http.MethodPatch, "/api/brands/1", http.StatusMethodNotAllowed},
		{http.MethodPatch, "/api/brands", http.StatusMethodNotAllowed},

		{http.MethodGet, "/api/taxes/1", http.StatusOK},
		{http.MethodGet, "/api/taxes/999", http.StatusNotFound},
		{http.MethodGet, "/api/taxes/abc", http.StatusBadRequest},
		{http.MethodDelete, "/api/taxes/1", http.StatusNoContent},
		{http.MethodPatch, "/api/taxes/1", http.StatusMethodNotAllowed},
		{http.MethodPatch, "/api/taxes", http.StatusMethodNotAllowed},

		{http.MethodGet, "/api/suppliers/1", http.StatusOK},
		{http.MethodGet, "/api/suppliers/999", http.StatusNotFound},
		{http.MethodGet, "/api/suppliers/abc", http.StatusBadRequest},
		{http.MethodDelete, "/api/suppliers/1", http.StatusNoContent},
		{http.MethodPatch, "/api/suppliers/1", http.StatusMethodNotAllowed},
		{http.MethodPatch, "/api/suppliers", http.StatusMethodNotAllowed},

		{http.MethodGet, "/api/categories/1", http.StatusOK},
		{http.MethodGet, "/api/categories/999", http.StatusNotFound},
		{http.MethodGet, "/api/categories/abc", http.StatusBadRequest},
		{http.MethodDelete, "/api/categories/1", http.StatusNoContent},
		{http.MethodPatch, "/api/categories/1", http.StatusMethodNotAllowed},
		{http.MethodPatch, "/api/categories", http.StatusMethodNotAllowed},

		{http.MethodGet, "/api/products/1", http.StatusOK},
		{http.MethodGet, "/api/products/999", http.StatusNotFound},
		{http.MethodGet, "/api/products/abc", http.StatusBadRequest},
		{http.MethodDelete, "/api/products/1", http.StatusNoContent},
		{http.MethodPatch, "/api/products/1", http.StatusMethodNotAllowed},
		{http.MethodPatch, "/api/products", http.StatusMethodNotAllowed},

		// Códigos de barra (fakeCodes: el producto 1 tiene el código 10).
		{http.MethodGet, "/api/products/1/codes", http.StatusOK},
		{http.MethodGet, "/api/products/999/codes", http.StatusNotFound},
		{http.MethodGet, "/api/products/abc/codes", http.StatusBadRequest},
		{http.MethodDelete, "/api/products/1/codes/10", http.StatusNoContent},
		{http.MethodDelete, "/api/products/1/codes/999", http.StatusNotFound},
		{http.MethodDelete, "/api/products/1/codes/abc", http.StatusBadRequest},
		{http.MethodPut, "/api/products/1/codes", http.StatusMethodNotAllowed},
		{http.MethodPatch, "/api/products/1/codes/10", http.StatusMethodNotAllowed},
	}

	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != tc.want {
				t.Fatalf("expected %d, got %d. Body: %s", tc.want, rec.Code, rec.Body.String())
			}
		})
	}
}
