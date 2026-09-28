package transport_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
)

type mockBrandSvc struct {
	getAllFn       func(ctx context.Context) ([]*model.Brand, error)
	getByIDFn      func(ctx context.Context, id int64) (*model.Brand, error)
	createFn       func(ctx context.Context, brand *model.Brand) (*model.Brand, error)
	updateFn       func(ctx context.Context, id int64, brand *model.Brand) (*model.Brand, error)
	deleteFn       func(ctx context.Context, id int64) error
	searchByNameFn func(ctx context.Context, query string) ([]*model.Brand, error)
}

func (m *mockBrandSvc) GetAllBrands(ctx context.Context) ([]*model.Brand, error) {
	return m.getAllFn(ctx)
}

func (m *mockBrandSvc) GetBrandByID(ctx context.Context, id int64) (*model.Brand, error) {
	return m.getByIDFn(ctx, id)
}

func (m *mockBrandSvc) CreateBrand(ctx context.Context, brand *model.Brand) (*model.Brand, error) {
	return m.createFn(ctx, brand)
}

func (m *mockBrandSvc) UpdateBrand(ctx context.Context, id int64, brand *model.Brand) (*model.Brand, error) {
	return m.updateFn(ctx, id, brand)
}

func (m *mockBrandSvc) DeleteBrand(ctx context.Context, id int64) error {
	return m.deleteFn(ctx, id)
}

func (m *mockBrandSvc) SearchByName(ctx context.Context, query string) ([]*model.Brand, error) {
	return m.searchByNameFn(ctx, query)
}

func TestBrandHandler_HandleBrands(t *testing.T) {
	t.Parallel()

	t.Run("GET returns all brands", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{
			getAllFn: func(_ context.Context) ([]*model.Brand, error) {
				return []*model.Brand{
					{ID: 1, Name: "Sony"},
				}, nil
			},
		})

		req := httptest.NewRequest(http.MethodGet, "/brands", nil)
		rec := httptest.NewRecorder()
		handler.HandleBrands(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d. Body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("GET search by name", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{
			searchByNameFn: func(_ context.Context, q string) ([]*model.Brand, error) {
				return []*model.Brand{
					{ID: 1, Name: "Sony"},
				}, nil
			},
		})

		req := httptest.NewRequest(http.MethodGet, "/brands?name=Sony", nil)
		rec := httptest.NewRecorder()
		handler.HandleBrands(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d. Body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("GET store failure returns 500", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{
			getAllFn: func(_ context.Context) ([]*model.Brand, error) {
				return nil, errors.New("db failure")
			},
		})

		req := httptest.NewRequest(http.MethodGet, "/brands", nil)
		rec := httptest.NewRecorder()
		handler.HandleBrands(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rec.Code)
		}
	})

	t.Run("POST invalid body returns 400", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{})

		req := httptest.NewRequest(http.MethodPost, "/brands", bytes.NewBufferString(`{invalid`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.HandleBrands(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("POST duplicate name returns 409", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{
			createFn: func(_ context.Context, _ *model.Brand) (*model.Brand, error) {
				return nil, service.ErrBrandAlreadyExists
			},
		})

		req := httptest.NewRequest(http.MethodPost, "/brands", bytes.NewBufferString(`{"name":"Sony"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.HandleBrands(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d", rec.Code)
		}
	})

	t.Run("POST success returns 201", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{
			createFn: func(_ context.Context, brand *model.Brand) (*model.Brand, error) {
				return &model.Brand{ID: 1, Name: brand.Name}, nil
			},
		})

		req := httptest.NewRequest(http.MethodPost, "/brands", bytes.NewBufferString(`{"name":"Sony"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.HandleBrands(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d. Body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("unsupported method returns 405", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{})

		req := httptest.NewRequest(http.MethodPatch, "/brands", nil)
		rec := httptest.NewRecorder()
		handler.HandleBrands(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
	})
}

func TestBrandHandler_HandleBrandByID(t *testing.T) {
	t.Parallel()

	t.Run("GET invalid id returns 400", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{})

		req := httptest.NewRequest(http.MethodGet, "/brand/abc", nil)
		rec := httptest.NewRecorder()
		handler.HandleBrandByID(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("GET not found returns 404", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{
			getByIDFn: func(_ context.Context, _ int64) (*model.Brand, error) {
				return nil, service.ErrBrandNotFound
			},
		})

		req := httptest.NewRequest(http.MethodGet, "/brand/999", nil)
		rec := httptest.NewRecorder()
		handler.HandleBrandByID(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("GET success returns 200", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{
			getByIDFn: func(_ context.Context, id int64) (*model.Brand, error) {
				return &model.Brand{ID: id, Name: "Sony"}, nil
			},
		})

		req := httptest.NewRequest(http.MethodGet, "/brand/1", nil)
		rec := httptest.NewRecorder()
		handler.HandleBrandByID(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("PUT invalid body returns 400", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{})

		req := httptest.NewRequest(http.MethodPut, "/brand/1", bytes.NewBufferString(`{invalid`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.HandleBrandByID(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("PUT not found returns 404", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{
			updateFn: func(_ context.Context, _ int64, _ *model.Brand) (*model.Brand, error) {
				return nil, service.ErrBrandNotFound
			},
		})

		req := httptest.NewRequest(http.MethodPut, "/brand/999", bytes.NewBufferString(`{"name":"Sony"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.HandleBrandByID(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("PUT duplicate name returns 409", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{
			updateFn: func(_ context.Context, _ int64, _ *model.Brand) (*model.Brand, error) {
				return nil, service.ErrBrandAlreadyExists
			},
		})

		req := httptest.NewRequest(http.MethodPut, "/brand/1", bytes.NewBufferString(`{"name":"Sony"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.HandleBrandByID(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d", rec.Code)
		}
	})

	t.Run("PUT success returns 200", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{
			updateFn: func(_ context.Context, id int64, brand *model.Brand) (*model.Brand, error) {
				return &model.Brand{ID: id, Name: brand.Name}, nil
			},
		})

		req := httptest.NewRequest(http.MethodPut, "/brand/1", bytes.NewBufferString(`{"name":"Sony"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.HandleBrandByID(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("DELETE not found returns 404", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{
			deleteFn: func(_ context.Context, _ int64) error {
				return service.ErrBrandNotFound
			},
		})

		req := httptest.NewRequest(http.MethodDelete, "/brand/999", nil)
		rec := httptest.NewRecorder()
		handler.HandleBrandByID(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("DELETE success returns 204", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{
			deleteFn: func(_ context.Context, _ int64) error {
				return nil
			},
		})

		req := httptest.NewRequest(http.MethodDelete, "/brand/1", nil)
		rec := httptest.NewRecorder()
		handler.HandleBrandByID(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
	})

	t.Run("unsupported method returns 405", func(t *testing.T) {
		handler := transport.NewBrandHandler(&mockBrandSvc{})

		req := httptest.NewRequest(http.MethodPatch, "/brand/1", nil)
		rec := httptest.NewRecorder()
		handler.HandleBrandByID(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
	})
}