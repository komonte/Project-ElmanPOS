package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"strings"
)

type mockBrandStore struct {
	createFn       func(ctx context.Context, brand *model.Brand) (*model.Brand, error)
	getAllFn       func(ctx context.Context) ([]*model.Brand, error)
	getByIDFn      func(ctx context.Context, id int64) (*model.Brand, error)
	searchByNameFn func(ctx context.Context, query string) ([]*model.Brand, error)
	updateFn       func(ctx context.Context, id int64, brand *model.Brand) (*model.Brand, error)
	deleteFn       func(ctx context.Context, id int64) error
}

func (m *mockBrandStore) Create(ctx context.Context, brand *model.Brand) (*model.Brand, error) {
	return m.createFn(ctx, brand)
}

func (m *mockBrandStore) GetAll(ctx context.Context) ([]*model.Brand, error) {
	return m.getAllFn(ctx)
}

func (m *mockBrandStore) GetByID(ctx context.Context, id int64) (*model.Brand, error) {
	return m.getByIDFn(ctx, id)
}

func (m *mockBrandStore) SearchByName(ctx context.Context, q string) ([]*model.Brand, error) {
	return m.searchByNameFn(ctx, q)
}

func (m *mockBrandStore) Update(ctx context.Context, id int64, brand *model.Brand) (*model.Brand, error) {
	return m.updateFn(ctx, id, brand)
}

func (m *mockBrandStore) Delete(ctx context.Context, id int64) error {
	return m.deleteFn(ctx, id)
}

func TestBrandService_GetAllBrands(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		mock    *mockBrandStore
		wantErr error
		wantLen int
	}{
		{
			name: "success",
			mock: &mockBrandStore{
				getAllFn: func(_ context.Context) ([]*model.Brand, error) {
					return []*model.Brand{
						{ID: 1, Name: "LOGITECH"},
						{ID: 2, Name: "RAZER"},
					}, nil
				},
			},
			wantErr: nil,
			wantLen: 2,
		},
		{
			name: "store failure",
			mock: &mockBrandStore{
				getAllFn: func(_ context.Context) ([]*model.Brand, error) {
					return nil, errStore
				},
			},
			wantErr: errStore,
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewBrandService(tt.mock)
			brands, err := svc.GetAllBrands(ctx)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if len(brands) != tt.wantLen {
				t.Errorf("len: got %d, want %d", len(brands), tt.wantLen)
			}
		})
	}
}

func TestBrandService_GetBrandByID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		id      int64
		mock    *mockBrandStore
		wantErr error
	}{
		{
			name:    "invalid id",
			id:      0,
			mock:    &mockBrandStore{},
			wantErr: service.ErrInvalidID,
		},
		{
			name: "not found",
			id:   1,
			mock: &mockBrandStore{
				getByIDFn: func(_ context.Context, _ int64) (*model.Brand, error) {
					return nil, store.ErrNotFound
				},
			},
			wantErr: service.ErrBrandNotFound,
		},
		{
			name: "store failure",
			id:   1,
			mock: &mockBrandStore{
				getByIDFn: func(_ context.Context, _ int64) (*model.Brand, error) {
					return nil, errStore
				},
			},
			wantErr: errStore,
		},
		{
			name: "success",
			id:   1,
			mock: &mockBrandStore{
				getByIDFn: func(_ context.Context, id int64) (*model.Brand, error) {
					return &model.Brand{ID: id, Name: "LOGITECH"}, nil
				},
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewBrandService(tt.mock)
			brand, err := svc.GetBrandByID(ctx, tt.id)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if brand.ID != tt.id {
				t.Errorf("id: got %d, want %d", brand.ID, tt.id)
			}
		})
	}
}

func TestBrandService_CreateBrand(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		input   *model.Brand
		mock    *mockBrandStore
		wantErr error
	}{
		{
			name:    "nil brand",
			input:   nil,
			mock:    &mockBrandStore{},
			wantErr: model.ErrNilBrand,
		},
		{
			name:    "empty name",
			input:   &model.Brand{Name: "  "},
			mock:    &mockBrandStore{},
			wantErr: model.ErrEmptyBrandName,
		},
		{
			name:    "short name",
			input:   &model.Brand{Name: "A"},
			mock:    &mockBrandStore{},
			wantErr: model.ErrBrandNameTooShort,
		},
		{
			name:    "long name",
			input:   &model.Brand{Name: strings.Repeat("A", 121)},
			mock:    &mockBrandStore{},
			wantErr: model.ErrBrandNameTooLong,
		},
		{
			name:  "duplicate name",
			input: &model.Brand{Name: "LOGITECH"},
			mock: &mockBrandStore{
				createFn: func(_ context.Context, _ *model.Brand) (*model.Brand, error) {
					return nil, store.ErrDuplicateBrandName
				},
			},
			wantErr: service.ErrBrandAlreadyExists,
		},
		{
			name:  "store failure",
			input: &model.Brand{Name: "LOGITECH"},
			mock: &mockBrandStore{
				createFn: func(_ context.Context, _ *model.Brand) (*model.Brand, error) {
					return nil, errStore
				},
			},
			wantErr: errStore,
		},
		{
			name:  "success",
			input: &model.Brand{Name: "LOGITECH"},
			mock: &mockBrandStore{
				createFn: func(_ context.Context, brand *model.Brand) (*model.Brand, error) {
					return &model.Brand{ID: 1, Name: brand.Name}, nil
				},
			},
			wantErr: nil,
		},
		{
			name:  "trims whitespace and uppercases",
			input: &model.Brand{Name: "  logitech  "},
			mock: &mockBrandStore{
				createFn: func(_ context.Context, brand *model.Brand) (*model.Brand, error) {
					if brand.Name != "LOGITECH" {
						t.Errorf("expected 'LOGITECH', got %q", brand.Name)
					}
					return &model.Brand{ID: 1, Name: brand.Name}, nil
				},
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewBrandService(tt.mock)
			created, err := svc.CreateBrand(ctx, tt.input)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if created == nil || created.ID == 0 {
				t.Fatal("expected valid brand with ID")
			}
		})
	}
}

func TestBrandService_UpdateBrand(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		id      int64
		input   *model.Brand
		mock    *mockBrandStore
		wantErr error
	}{
		{
			name:    "invalid id",
			id:      0,
			input:   &model.Brand{Name: "LOGITECH"},
			mock:    &mockBrandStore{},
			wantErr: service.ErrInvalidID,
		},
		{
			name:    "empty name",
			id:      1,
			input:   &model.Brand{Name: "  "},
			mock:    &mockBrandStore{},
			wantErr: model.ErrEmptyBrandName,
		},
		{
			name:  "not found",
			id:    1,
			input: &model.Brand{Name: "LOGITECH"},
			mock: &mockBrandStore{
				updateFn: func(_ context.Context, _ int64, _ *model.Brand) (*model.Brand, error) {
					return nil, store.ErrNotFound
				},
			},
			wantErr: service.ErrBrandNotFound,
		},
		{
			name:  "duplicate name",
			id:    1,
			input: &model.Brand{Name: "LOGITECH"},
			mock: &mockBrandStore{
				updateFn: func(_ context.Context, _ int64, _ *model.Brand) (*model.Brand, error) {
					return nil, store.ErrDuplicateBrandName
				},
			},
			wantErr: service.ErrBrandAlreadyExists,
		},
		{
			name:  "store failure",
			id:    1,
			input: &model.Brand{Name: "LOGITECH"},
			mock: &mockBrandStore{
				updateFn: func(_ context.Context, _ int64, _ *model.Brand) (*model.Brand, error) {
					return nil, errStore
				},
			},
			wantErr: errStore,
		},
		{
			name:  "success",
			id:    1,
			input: &model.Brand{Name: "LOGITECH"},
			mock: &mockBrandStore{
				updateFn: func(_ context.Context, id int64, brand *model.Brand) (*model.Brand, error) {
					return &model.Brand{ID: id, Name: brand.Name}, nil
				},
			},
			wantErr: nil,
		},
		{
			name:  "trims whitespace and uppercases",
			id:    1,
			input: &model.Brand{Name: "  logitech  "},
			mock: &mockBrandStore{
				updateFn: func(_ context.Context, id int64, brand *model.Brand) (*model.Brand, error) {
					if brand.Name != "LOGITECH" {
						t.Errorf("expected 'LOGITECH', got %q", brand.Name)
					}
					return &model.Brand{ID: id, Name: brand.Name}, nil
				},
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewBrandService(tt.mock)
			updated, err := svc.UpdateBrand(ctx, tt.id, tt.input)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if updated == nil || updated.ID == 0 {
				t.Fatal("expected valid brand with ID")
			}
		})
	}
}

func TestBrandService_DeleteBrand(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		id      int64
		mock    *mockBrandStore
		wantErr error
	}{
		{
			name:    "invalid id",
			id:      0,
			mock:    &mockBrandStore{},
			wantErr: service.ErrInvalidID,
		},
		{
			name: "not found",
			id:   1,
			mock: &mockBrandStore{
				deleteFn: func(_ context.Context, _ int64) error {
					return store.ErrNotFound
				},
			},
			wantErr: service.ErrBrandNotFound,
		},
		{
			name: "store failure",
			id:   1,
			mock: &mockBrandStore{
				deleteFn: func(_ context.Context, _ int64) error {
					return errStore
				},
			},
			wantErr: errStore,
		},
		{
			name: "success",
			id:   1,
			mock: &mockBrandStore{
				deleteFn: func(_ context.Context, _ int64) error {
					return nil
				},
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewBrandService(tt.mock)
			err := svc.DeleteBrand(ctx, tt.id)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestBrandService_SearchByName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		query   string
		mock    *mockBrandStore
		wantErr error
		wantLen int
	}{
		{
			name:    "empty query",
			query:   "",
			mock:    &mockBrandStore{},
			wantErr: nil,
			wantLen: 0,
		},
		{
			name:    "whitespace query",
			query:   "  ",
			mock:    &mockBrandStore{},
			wantErr: nil,
			wantLen: 0,
		},
		{
			name:  "success",
			query: "LOGI",
			mock: &mockBrandStore{
				searchByNameFn: func(_ context.Context, _ string) ([]*model.Brand, error) {
					return []*model.Brand{{ID: 1, Name: "LOGITECH"}}, nil
				},
			},
			wantErr: nil,
			wantLen: 1,
		},
		{
			name:  "store failure",
			query: "LOGI",
			mock: &mockBrandStore{
				searchByNameFn: func(_ context.Context, _ string) ([]*model.Brand, error) {
					return nil, errStore
				},
			},
			wantErr: errStore,
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewBrandService(tt.mock)
			brands, err := svc.SearchByName(ctx, tt.query)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if len(brands) != tt.wantLen {
				t.Errorf("len: got %d, want %d", len(brands), tt.wantLen)
			}
		})
	}
}