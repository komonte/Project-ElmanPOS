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
	"github.com/shopspring/decimal"
)

type mockTaxSvc struct {
	getAllFn       func(ctx context.Context) ([]*model.Tax, error)
	getByIDFn      func(ctx context.Context, id int64) (*model.Tax, error)
	createFn       func(ctx context.Context, tax *model.Tax) (*model.Tax, error)
	updateFn       func(ctx context.Context, tax *model.Tax) (*model.Tax, error)
	deleteFn       func(ctx context.Context, id int64) error
	searchByNameFn func(ctx context.Context, query string) ([]*model.Tax, error)
}

func (m *mockTaxSvc) GetAllTaxes(ctx context.Context) ([]*model.Tax, error) {
	return m.getAllFn(ctx)
}

func (m *mockTaxSvc) GetTaxByID(ctx context.Context, id int64) (*model.Tax, error) {
	return m.getByIDFn(ctx, id)
}

func (m *mockTaxSvc) CreateTax(ctx context.Context, tax *model.Tax) (*model.Tax, error) {
	return m.createFn(ctx, tax)
}

func (m *mockTaxSvc) UpdateTax(ctx context.Context, tax *model.Tax) (*model.Tax, error) {
	return m.updateFn(ctx, tax)
}

func (m *mockTaxSvc) DeleteTax(ctx context.Context, id int64) error {
	return m.deleteFn(ctx, id)
}

func (m *mockTaxSvc) SearchByName(ctx context.Context, query string) ([]*model.Tax, error) {
	return m.searchByNameFn(ctx, query)
}

func TestTaxHandler_HandleTaxes(t *testing.T) {
	t.Parallel()

	t.Run("GET returns all taxes", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{
			getAllFn: func(_ context.Context) ([]*model.Tax, error) {
				return []*model.Tax{
					{ID: 1, Name: "IVA 21%", Rate: decimal.NewFromInt(21)},
				}, nil
			},
		})

		req := httptest.NewRequest(http.MethodGet, "/taxes", nil)
		rec := httptest.NewRecorder()
		handler.HandleTaxes(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d. Body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("GET search by name", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{
			searchByNameFn: func(_ context.Context, q string) ([]*model.Tax, error) {
				return []*model.Tax{
					{ID: 1, Name: "IVA 21%", Rate: decimal.NewFromInt(21)},
				}, nil
			},
		})

		req := httptest.NewRequest(http.MethodGet, "/taxes?name=IVA", nil)
		rec := httptest.NewRecorder()
		handler.HandleTaxes(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d. Body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("GET store failure returns 500", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{
			getAllFn: func(_ context.Context) ([]*model.Tax, error) {
				return nil, errors.New("db failure")
			},
		})

		req := httptest.NewRequest(http.MethodGet, "/taxes", nil)
		rec := httptest.NewRecorder()
		handler.HandleTaxes(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d", rec.Code)
		}
	})

	t.Run("POST invalid body returns 400", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{})

		req := httptest.NewRequest(http.MethodPost, "/taxes", bytes.NewBufferString(`{invalid`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.HandleTaxes(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("POST duplicate name returns 409", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{
			createFn: func(_ context.Context, _ *model.Tax) (*model.Tax, error) {
				return nil, service.ErrTaxAlreadyExists
			},
		})

		req := httptest.NewRequest(http.MethodPost, "/taxes", bytes.NewBufferString(`{"name":"IVA","rate":21}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.HandleTaxes(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d", rec.Code)
		}
	})

	t.Run("POST success returns 201", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{
			createFn: func(_ context.Context, tax *model.Tax) (*model.Tax, error) {
				return &model.Tax{ID: 1, Name: tax.Name, Rate: tax.Rate}, nil
			},
		})

		req := httptest.NewRequest(http.MethodPost, "/taxes", bytes.NewBufferString(`{"name":"IVA","rate":21}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.HandleTaxes(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d. Body: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("unsupported method returns 405", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{})

		req := httptest.NewRequest(http.MethodPatch, "/taxes", nil)
		rec := httptest.NewRecorder()
		handler.HandleTaxes(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
	})
}

func TestTaxHandler_HandleTaxByID(t *testing.T) {
	t.Parallel()

	t.Run("GET invalid id returns 400", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{})

		req := httptest.NewRequest(http.MethodGet, "/tax/abc", nil)
		rec := httptest.NewRecorder()
		handler.HandleTaxByID(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("GET not found returns 404", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{
			getByIDFn: func(_ context.Context, _ int64) (*model.Tax, error) {
				return nil, service.ErrTaxNotFound
			},
		})

		req := httptest.NewRequest(http.MethodGet, "/tax/999", nil)
		rec := httptest.NewRecorder()
		handler.HandleTaxByID(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("GET success returns 200", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{
			getByIDFn: func(_ context.Context, id int64) (*model.Tax, error) {
				return &model.Tax{ID: id, Name: "IVA 21%", Rate: decimal.NewFromInt(21)}, nil
			},
		})

		req := httptest.NewRequest(http.MethodGet, "/tax/1", nil)
		rec := httptest.NewRecorder()
		handler.HandleTaxByID(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("PUT invalid body returns 400", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{})

		req := httptest.NewRequest(http.MethodPut, "/tax/1", bytes.NewBufferString(`{invalid`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.HandleTaxByID(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d", rec.Code)
		}
	})

	t.Run("PUT not found returns 404", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{
			updateFn: func(_ context.Context, _ *model.Tax) (*model.Tax, error) {
				return nil, service.ErrTaxNotFound
			},
		})

		req := httptest.NewRequest(http.MethodPut, "/tax/999", bytes.NewBufferString(`{"name":"IVA","rate":21}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.HandleTaxByID(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("PUT duplicate name returns 409", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{
			updateFn: func(_ context.Context, _ *model.Tax) (*model.Tax, error) {
				return nil, service.ErrTaxAlreadyExists
			},
		})

		req := httptest.NewRequest(http.MethodPut, "/tax/1", bytes.NewBufferString(`{"name":"IVA","rate":21}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.HandleTaxByID(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("expected 409, got %d", rec.Code)
		}
	})

	t.Run("PUT success returns 200", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{
			updateFn: func(_ context.Context, tax *model.Tax) (*model.Tax, error) {
				return &model.Tax{ID: tax.ID, Name: tax.Name, Rate: tax.Rate}, nil
			},
		})

		req := httptest.NewRequest(http.MethodPut, "/tax/1", bytes.NewBufferString(`{"name":"IVA","rate":21}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.HandleTaxByID(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("DELETE not found returns 404", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{
			deleteFn: func(_ context.Context, _ int64) error {
				return service.ErrTaxNotFound
			},
		})

		req := httptest.NewRequest(http.MethodDelete, "/tax/999", nil)
		rec := httptest.NewRecorder()
		handler.HandleTaxByID(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("expected 404, got %d", rec.Code)
		}
	})

	t.Run("DELETE success returns 204", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{
			deleteFn: func(_ context.Context, _ int64) error {
				return nil
			},
		})

		req := httptest.NewRequest(http.MethodDelete, "/tax/1", nil)
		rec := httptest.NewRecorder()
		handler.HandleTaxByID(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rec.Code)
		}
	})

	t.Run("unsupported method returns 405", func(t *testing.T) {
		handler := transport.NewTaxHandler(&mockTaxSvc{})

		req := httptest.NewRequest(http.MethodPatch, "/tax/1", nil)
		rec := httptest.NewRecorder()
		handler.HandleTaxByID(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405, got %d", rec.Code)
		}
	})
}
