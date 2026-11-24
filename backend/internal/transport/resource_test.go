package transport_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/shopspring/decimal"
)

var errStore = errors.New("store failure")

type MockStore[T any] struct {
	createFn       func(ctx context.Context, entity *T) (*T, error)
	getAllFn       func(ctx context.Context) ([]*T, error)
	getByIDFn      func(ctx context.Context, id int64) (*T, error)
	searchByNameFn func(ctx context.Context, q string) ([]*T, error)
	updateFn       func(ctx context.Context, id int64, entity *T) (*T, error)
	deleteFn       func(ctx context.Context, id int64) error

	getAllIncludingInactiveFn func(ctx context.Context) ([]*T, error)
	searchAllByNameFn         func(ctx context.Context, q string) ([]*T, error)
	setActiveFn               func(ctx context.Context, id int64, active bool) error
}

func (m *MockStore[T]) Create(ctx context.Context, entity *T) (*T, error)  { return m.createFn(ctx, entity) }
func (m *MockStore[T]) GetAll(ctx context.Context) ([]*T, error)            { return m.getAllFn(ctx) }
func (m *MockStore[T]) GetByID(ctx context.Context, id int64) (*T, error)    { return m.getByIDFn(ctx, id) }
func (m *MockStore[T]) SearchByName(ctx context.Context, q string) ([]*T, error) { return m.searchByNameFn(ctx, q) }
func (m *MockStore[T]) Update(ctx context.Context, id int64, entity *T) (*T, error) { return m.updateFn(ctx, id, entity) }
func (m *MockStore[T]) Delete(ctx context.Context, id int64) error            { return m.deleteFn(ctx, id) }
func (m *MockStore[T]) GetAllIncludingInactive(ctx context.Context) ([]*T, error) {
	return m.getAllIncludingInactiveFn(ctx)
}
func (m *MockStore[T]) SearchAllByName(ctx context.Context, q string) ([]*T, error) {
	return m.searchAllByNameFn(ctx, q)
}
func (m *MockStore[T]) SetActive(ctx context.Context, id int64, active bool) error {
	return m.setActiveFn(ctx, id, active)
}

// serve routes req through a ServeMux wired with Register, the same way
// RegisterAPIRoutes does in main.go, instead of calling handlers directly.
func serve[T any](h *transport.ResourceHandler[T], base string, rec *httptest.ResponseRecorder, req *http.Request) {
	mux := http.NewServeMux()
	h.Register(mux, base)
	mux.ServeHTTP(rec, req)
}

func runHandlerTests[T any](t *testing.T, label, base string, entity *T) {
	t.Run("GET collection returns all", func(t *testing.T) {
		mock := &MockStore[T]{
			getAllFn: func(_ context.Context) ([]*T, error) {
				return []*T{entity, entity}, nil
			},
		}
		svc := service.NewService[T](mock, nil)
		h := transport.NewResourceHandler[T](svc, label)

		req := httptest.NewRequest(http.MethodGet, base, nil)
		rec := httptest.NewRecorder()
		serve(h, base, rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d. Body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("GET collection returns empty list", func(t *testing.T) {
		mock := &MockStore[T]{
			getAllFn: func(_ context.Context) ([]*T, error) {
				return make([]*T, 0), nil
			},
		}
		svc := service.NewService[T](mock, nil)
		h := transport.NewResourceHandler[T](svc, label)

		req := httptest.NewRequest(http.MethodGet, base, nil)
		rec := httptest.NewRecorder()
		serve(h, base, rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d. Body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("GET collection store failure returns 500", func(t *testing.T) {
		mock := &MockStore[T]{
			getAllFn: func(_ context.Context) ([]*T, error) {
				return nil, errStore
			},
		}
		svc := service.NewService[T](mock, nil)
		h := transport.NewResourceHandler[T](svc, label)

		req := httptest.NewRequest(http.MethodGet, base, nil)
		rec := httptest.NewRecorder()
		serve(h, base, rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rec.Code)
		}
	})

	t.Run("GET search by name", func(t *testing.T) {
		mock := &MockStore[T]{
			searchByNameFn: func(_ context.Context, _ string) ([]*T, error) {
				return []*T{entity}, nil
			},
		}
		svc := service.NewService[T](mock, nil)
		h := transport.NewResourceHandler[T](svc, label)

		req := httptest.NewRequest(http.MethodGet, base + "?name=test", nil)
		rec := httptest.NewRecorder()
		serve(h, base, rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d. Body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("GET by id success", func(t *testing.T) {
		mock := &MockStore[T]{
			getByIDFn: func(_ context.Context, _ int64) (*T, error) {
				return entity, nil
			},
		}
		svc := service.NewService[T](mock, nil)
		h := transport.NewResourceHandler[T](svc, label)

		req := httptest.NewRequest(http.MethodGet, base + "/1", nil)
		rec := httptest.NewRecorder()
		serve(h, base, rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("DELETE success returns 204", func(t *testing.T) {
		mock := &MockStore[T]{
			deleteFn: func(_ context.Context, _ int64) error { return nil },
		}
		svc := service.NewService[T](mock, nil)
		h := transport.NewResourceHandler[T](svc, label)

		req := httptest.NewRequest(http.MethodDelete, base + "/1", nil)
		rec := httptest.NewRecorder()
		serve(h, base, rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
	})

	t.Run("DELETE not found returns 404", func(t *testing.T) {
		mock := &MockStore[T]{
			deleteFn: func(_ context.Context, _ int64) error { return store.ErrNotFound },
		}
		svc := service.NewService[T](mock, nil)
		h := transport.NewResourceHandler[T](svc, label)

		req := httptest.NewRequest(http.MethodDelete, base + "/999", nil)
		rec := httptest.NewRecorder()
		serve(h, base, rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("DELETE in use returns 409", func(t *testing.T) {
		mock := &MockStore[T]{
			deleteFn: func(_ context.Context, _ int64) error { return store.ErrInUse },
		}
		svc := service.NewService[T](mock, nil)
		h := transport.NewResourceHandler[T](svc, label)

		req := httptest.NewRequest(http.MethodDelete, base + "/1", nil)
		rec := httptest.NewRecorder()
		serve(h, base, rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d", rec.Code)
		}
	})

	// Crear y editar: cada error del service tiene su código. Un error que
	// no se reconoce (ej. la DB caída) es 500, no 400: no es culpa del pedido.
	body, err := json.Marshal(entity)
	if err != nil {
		t.Fatalf("marshal entity: %v", err)
	}
	errBadData := errors.New("bad data")

	writeCases := []struct {
		name     string
		validate func(*T) error
		storeErr error
		want     int
	}{
		{"success", nil, nil, 0}, // 201 al crear, 200 al editar
		{"validation error returns 400", func(*T) error { return errBadData }, nil, http.StatusBadRequest},
		{"invalid reference returns 400", nil, store.ErrInvalidReference, http.StatusBadRequest},
		{"duplicate returns 409", nil, store.ErrDuplicateName, http.StatusConflict},
		{"rule conflict returns 409", nil, store.ErrCategoryCycle, http.StatusConflict},
		{"store failure returns 500", nil, errStore, http.StatusInternalServerError},
	}
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		for _, tc := range writeCases {
			t.Run(method+" "+tc.name, func(t *testing.T) {
				mock := &MockStore[T]{
					createFn: func(_ context.Context, e *T) (*T, error) {
						if tc.storeErr != nil {
							return nil, tc.storeErr
						}
						return e, nil
					},
					updateFn: func(_ context.Context, _ int64, e *T) (*T, error) {
						if tc.storeErr != nil {
							return nil, tc.storeErr
						}
						return e, nil
					},
				}
				h := transport.NewResourceHandler[T](service.NewService[T](mock, tc.validate), label)

				path, want := base, tc.want
				if method == http.MethodPut {
					path = base + "/1"
				}
				if want == 0 {
					want = map[string]int{http.MethodPost: http.StatusCreated, http.MethodPut: http.StatusOK}[method]
				}

				req := httptest.NewRequest(method, path, bytes.NewReader(body))
				rec := httptest.NewRecorder()
				serve(h, base, rec, req)

				if rec.Code != want {
					t.Fatalf("expected %d, got %d. Body: %s", want, rec.Code, rec.Body.String())
				}
			})
		}
	}

	t.Run("validation error message says what is wrong", func(t *testing.T) {
		h := transport.NewResourceHandler[T](service.NewService[T](&MockStore[T]{}, func(*T) error { return errBadData }), label)
		req := httptest.NewRequest(http.MethodPost, base, bytes.NewReader(body))
		rec := httptest.NewRecorder()
		serve(h, base, rec, req)
		if !strings.Contains(rec.Body.String(), "bad data") {
			t.Fatalf("body %s does not explain the error", rec.Body.String())
		}
	})

	t.Run("invalid id returns 400", func(t *testing.T) {
		h := transport.NewResourceHandler[T](service.NewService[T](&MockStore[T]{}, nil), label)

		req := httptest.NewRequest(http.MethodGet, base + "/abc", nil)
		rec := httptest.NewRecorder()
		serve(h, base, rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("unsupported method returns 405", func(t *testing.T) {
		h := transport.NewResourceHandler[T](service.NewService[T](&MockStore[T]{}, nil), label)

		req := httptest.NewRequest(http.MethodPatch, base, nil)
		rec := httptest.NewRecorder()
		serve(h, base, rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
	})
}

func TestHandlerWithBrand(t *testing.T) {
	t.Parallel()
	runHandlerTests[model.Brand](t, "brand", "/api/brands", &model.Brand{
		ID: 1, Name: "Sony",
	})
}

func TestHandlerWithTax(t *testing.T) {
	t.Parallel()
	runHandlerTests[model.Tax](t, "tax", "/api/taxes", &model.Tax{
		ID: 1, Name: "IVA", Rate: decimal.NewFromInt(21),
	})
}
func TestHandlerWithSupplier(t *testing.T) {
	t.Parallel()
	runHandlerTests[model.Supplier](t, "supplier", "/api/suppliers", &model.Supplier{
		ID: 1, Name: "Arcor",
	})
}

func TestHandlerWithCategory(t *testing.T) {
	t.Parallel()
	runHandlerTests[model.Category](t, "category", "/api/categories", &model.Category{
		ID: 1, Name: "Limpieza", TaxID: 1,
	})
}
