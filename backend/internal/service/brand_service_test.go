package service_test

import (
	"errors"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
)

type mockStore struct {
	getAllFn	func() ([]*model.Brand, error)
	getByIDFn	func(id int64) (*model.Brand, error)
	createFn	func(brand *model.Brand) (*model.Brand, error)
	updateFn	func(id int64, brand *model.Brand) (*model.Brand, error)
	deleteFn	func(id int64) error
	searchByNameFn	func(query string) ([]*model.Brand, error)
}

func (m *mockStore) GetAll() ([]*model.Brand, error)                  { return m.getAllFn() }
func (m *mockStore) GetByID(id int64) (*model.Brand, error)          { return m.getByIDFn(id) }
func (m *mockStore) Create(brand *model.Brand) (*model.Brand, error) { return m.createFn(brand) }
func (m *mockStore) Update(id int64, b *model.Brand) (*model.Brand, error) {
	return m.updateFn(id, b)
}
func (m *mockStore) Delete(id int64) error                         { return m.deleteFn(id) }
func (m *mockStore) SearchByName(q string) ([]*model.Brand, error) { return m.searchByNameFn(q) }

func TestService_CreateBrand(t *testing.T)  {
	tests := []struct {
		name	string
		input	*model.Brand
		mockStore	*mockStore
		expectErr	bool
		expectedErr	string
	}{
		{
			name:	"error when brand is nil",
			input:	nil,
			mockStore: &mockStore{},
			expectErr: true,
			expectedErr: "brand data is required",
		},
		{
			name:	"error when name is empty",
			input:	&model.Brand{Name: "   "},
			mockStore:	&mockStore{},
			expectErr: true,
			expectedErr: "brand name is required",
		},
		{
			name:	"error when name is shorter than 2 chars",
			input:	&model.Brand{Name: "A"},
			mockStore: &mockStore{},
			expectErr: true,
			expectedErr: "brand name must have at least 2 characters",
		},
		{
			name: "success creates brand and trims whitespace",
			input: &model.Brand{Name: " Logitech "},
			mockStore: &mockStore{
				createFn: func(b *model.Brand) (*model.Brand, error) {
					if b.Name != "Logitech" {
						t.Errorf("expected trimmed name 'logitech', got '%s'", b.Name)
					}
					return &model.Brand{ID: 1, Name: b.Name}, nil
				},
			},
			expectErr: false,
		},
		{
			name: "error propagated from store failure",
			input: &model.Brand{Name: "Samsung"},
			mockStore: &mockStore{
				createFn: func(b *model.Brand) (*model.Brand, error){
					return nil, errors.New("db connection failure")
				},
			},
			expectErr: true,
			expectedErr: "db connection failure",
		},

	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.New(tt.mockStore)
			result, err := svc.CreateBrand(tt.input)

			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				if err.Error() != tt.expectedErr {
					t.Errorf("expected error '%s', got '%s'", tt.expectedErr, err.Error())
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result == nil || result.ID == 0 {
				t.Errorf("expected valid brand result, got %v", result)
			}
		})
	}
}

func TestService_GetBrandByID(t *testing.T) {
	tests:= []struct {
		name      string
		id        int64
		mockStore *mockStore
		expectErr bool
	}{
		{
			name:      "error when id is zero or negative",
			id:        0,
			mockStore: &mockStore{},
			expectErr: true,
		},
		{
			name: "success when id is valid",
			id:   5,
			mockStore: &mockStore{
				getByIDFn: func(id int64) (*model.Brand, error) {
					return &model.Brand{ID: 5, Name: "Razer"}, nil
				},
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.New(tt.mockStore)
			_, err := svc.GetBrandByID(tt.id)

			if (err != nil) != tt.expectErr {
				t.Fatalf("expected error: %v, got: %v", tt.expectErr, err)
			}
		})
	}
}

func TestService_SearchByName(t *testing.T) {
	t.Run("returns empty slice without querying store if empty query", func(t *testing.T) {
		mockCalled := false
		mock := &mockStore{
			searchByNameFn: func(q string) ([]*model.Brand, error) {
				mockCalled = true
				return nil, nil
			},
		}

		svc := service.New(mock)
		res, err := svc.SearchByName("   ")

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if mockCalled {
			t.Error("expected store NOT to be called for empty search")
		}
		if len(res) != 0 {
			t.Errorf("expected empty slice, got length %d", len(res))
		}
	})
}



