package transport_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
}

func (m *MockStore[T]) Create(ctx context.Context, entity *T) (*T, error)  { return m.createFn(ctx, entity) }
func (m *MockStore[T]) GetAll(ctx context.Context) ([]*T, error)            { return m.getAllFn(ctx) }
func (m *MockStore[T]) GetByID(ctx context.Context, id int64) (*T, error)    { return m.getByIDFn(ctx, id) }
func (m *MockStore[T]) SearchByName(ctx context.Context, q string) ([]*T, error) { return m.searchByNameFn(ctx, q) }
func (m *MockStore[T]) Update(ctx context.Context, id int64, entity *T) (*T, error) { return m.updateFn(ctx, id, entity) }
func (m *MockStore[T]) Delete(ctx context.Context, id int64) error            { return m.deleteFn(ctx, id) }

func runHandlerTests[T any](t *testing.T, label string, entity *T) {
	t.Run("GET collection returns all", func(t *testing.T) {
		mock := &MockStore[T]{
			getAllFn: func(_ context.Context) ([]*T, error) {
				return []*T{entity, entity}, nil
			},
		}
		svc := service.NewService[T](mock, nil)
		h := transport.NewResourceHandler[T](svc, label)

		req := httptest.NewRequest(http.MethodGet, "/" + label + "s", nil)
		rec := httptest.NewRecorder()
		h.HandleCollection(rec, req)

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

		req := httptest.NewRequest(http.MethodGet, "/" + label + "s", nil)
		rec := httptest.NewRecorder()
		h.HandleCollection(rec, req)

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

		req := httptest.NewRequest(http.MethodGet, "/" + label + "s", nil)
		rec := httptest.NewRecorder()
		h.HandleCollection(rec, req)

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

		req := httptest.NewRequest(http.MethodGet, "/" + label + "s?name=test", nil)
		rec := httptest.NewRecorder()
		h.HandleCollection(rec, req)

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

		req := httptest.NewRequest(http.MethodGet, "/" + label + "/1", nil)
		rec := httptest.NewRecorder()
		h.HandleEntity(rec, req)

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

		req := httptest.NewRequest(http.MethodDelete, "/" + label + "/1", nil)
		rec := httptest.NewRecorder()
		h.HandleEntity(rec, req)

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

		req := httptest.NewRequest(http.MethodDelete, "/" + label + "/999", nil)
		rec := httptest.NewRecorder()
		h.HandleEntity(rec, req)

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

		req := httptest.NewRequest(http.MethodDelete, "/" + label + "/1", nil)
		rec := httptest.NewRecorder()
		h.HandleEntity(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d", rec.Code)
		}
	})

	t.Run("invalid id returns 400", func(t *testing.T) {
		h := transport.NewResourceHandler[T](service.NewService[T](&MockStore[T]{}, nil), label)

		req := httptest.NewRequest(http.MethodGet, "/" + label + "/abc", nil)
		rec := httptest.NewRecorder()
		h.HandleEntity(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("unsupported method returns 405", func(t *testing.T) {
		h := transport.NewResourceHandler[T](service.NewService[T](&MockStore[T]{}, nil), label)

		req := httptest.NewRequest(http.MethodPatch, "/" + label + "s", nil)
		rec := httptest.NewRecorder()
		h.HandleCollection(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
	})
}

func TestHandlerWithBrand(t *testing.T) {
	t.Parallel()
	runHandlerTests[model.Brand](t, "brand", &model.Brand{
		ID: 1, Name: "Sony",
	})
}

func TestHandlerWithTax(t *testing.T) {
	t.Parallel()
	runHandlerTests[model.Tax](t, "tax", &model.Tax{
		ID: 1, Name: "IVA", Rate: decimal.NewFromInt(21),
	})
}