package transport_test

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"database/sql"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
)

type mockService struct {
	getAllBrandsFn func() ([]*model.Brand, error)
	getBrandByIDFn func(id int64) (*model.Brand, error)
	createBrandFn  func(b *model.Brand) (*model.Brand, error)
	updateBrandFn  func(id int64, b *model.Brand) (*model.Brand, error)
	deleteBrandFn  func(id int64) error
	searchByNameFn func(query string) ([]*model.Brand, error)
}

func (m *mockService) GetAllBrands() ([]*model.Brand, error) { return m.getAllBrandsFn() }
func (m *mockService) GetBrandByID(id int64) (*model.Brand, error) {
	return m.getBrandByIDFn(id)
}
func (m *mockService) CreateBrand(b *model.Brand) (*model.Brand, error) {
	return m.createBrandFn(b)
}
func (m *mockService) UpdateBrand(id int64, b *model.Brand) (*model.Brand, error) {
	return m.updateBrandFn(id, b)
}
func (m *mockService) DeleteBrand(id int64) error { return m.deleteBrandFn(id) }
func (m *mockService) SearchByName(q string) ([]*model.Brand, error) {
	return m.searchByNameFn(q)
}

func TestBrandHandler_HandleBrands_Create(t *testing.T) {
	tests := []struct {
		name           string
		body           string
		mockSvc        *mockService
		expectedStatus int
	}{
		{
			name:           "error invalid json body",
			body:           `{"name": `,
			mockSvc:        &mockService{},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "error business validation failure",
			body: `{"name": "A"}`,
			mockSvc: &mockService{
				createBrandFn: func(b *model.Brand) (*model.Brand, error) {
					return nil, errors.New("brand name must have at least 2 characters")
				},
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "success creates brand",
			body: `{"name": "Logitech"}`,
			mockSvc: &mockService{
				createBrandFn: func(b *model.Brand) (*model.Brand, error) {
					return &model.Brand{ID: 1, Name: b.Name}, nil
				},
			},
			expectedStatus: http.StatusCreated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := transport.NewBrandHandler(tt.mockSvc)

			req := httptest.NewRequest(http.MethodPost, "/brands", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			handler.HandleBrands(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Fatalf("expected status %d, got %d. Body: %s", tt.expectedStatus, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestBrandHandler_HandleBrandByID_Get(t *testing.T) {
	tests := []struct {
		name           string
		targetURL      string
		mockSvc        *mockService
		expectedStatus int
	}{
		{
			name:           "error invalid id in url",
			targetURL:      "/brand/abc",
			mockSvc:        &mockService{},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:      "error not found",
			targetURL: "/brand/999",
			mockSvc: &mockService{
				getBrandByIDFn: func(id int64) (*model.Brand, error) {
					return nil, sql.ErrNoRows
				},
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:      "success returns brand",
			targetURL: "/brand/1",
			mockSvc: &mockService{
				getBrandByIDFn: func(id int64) (*model.Brand, error) {
					return &model.Brand{ID: 1, Name: "Sony"}, nil
				},
			},
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := transport.NewBrandHandler(tt.mockSvc)

			req := httptest.NewRequest(http.MethodGet, tt.targetURL, nil)
			rec := httptest.NewRecorder()

			handler.HandleBrandByID(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Fatalf("expected status %d, got %d", tt.expectedStatus, rec.Code)
			}
		})
	}
}
