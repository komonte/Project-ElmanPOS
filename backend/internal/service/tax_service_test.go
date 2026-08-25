package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/shopspring/decimal"
)

type mockTaxStore struct {
	createFn       func(ctx context.Context, tax *model.Tax) (*model.Tax, error)
	getAllFn       func(ctx context.Context) ([]*model.Tax, error)
	getByIDFn      func(ctx context.Context, id int64) (*model.Tax, error)
	searchByNameFn func(ctx context.Context, query string) ([]*model.Tax, error)
	updateFn       func(ctx context.Context, id int64, tax *model.Tax) (*model.Tax, error)
	deleteFn       func(ctx context.Context, id int64) error
}

func (m *mockTaxStore) Create(ctx context.Context, tax *model.Tax) (*model.Tax, error) {
	return m.createFn(ctx, tax)
}

func (m *mockTaxStore) GetAll(ctx context.Context) ([]*model.Tax, error) {
	return m.getAllFn(ctx)
}

func (m *mockTaxStore) GetByID(ctx context.Context, id int64) (*model.Tax, error) {
	return m.getByIDFn(ctx, id)
}

func (m *mockTaxStore) SearchByName(ctx context.Context, q string) ([]*model.Tax, error) {
	return m.searchByNameFn(ctx, q)
}

func (m *mockTaxStore) Update(ctx context.Context, id int64, b *model.Tax) (*model.Tax, error) {
	return m.updateFn(ctx, id, b)
}

func (m *mockTaxStore) Delete(ctx context.Context, id int64) error {
	return m.deleteFn(ctx, id)
}

// verifyError asserts that gotErr matches wantErr using errors.Is.
// Returns true on success path (wantErr == nil), false on error path.
func verifyError(t *testing.T, wantErr, gotErr error) bool {
	t.Helper()
	if wantErr != nil {
		if gotErr == nil {
			t.Fatalf("expected error %v, got nil", wantErr)
		}
		if !errors.Is(gotErr, wantErr) {
			t.Errorf("expected error %v, got %v", wantErr, gotErr)
		}
		return false
	}
	if gotErr != nil {
		t.Fatalf("unexpected error: %v", gotErr)
	}
	return true
}

// TestTaxService_Validate exhaustively tests all validation rules once.
func TestTaxService_Validate(t *testing.T) {
	ctx := context.Background()

	successStore := &mockTaxStore{
		createFn: func(_ context.Context, tax *model.Tax) (*model.Tax, error) {
			return &model.Tax{ID: 1, Name: tax.Name, Rate: tax.Rate}, nil
		},
	}

	tests := []struct {
		name    string
		input   *model.Tax
		store   *mockTaxStore
		wantErr error
	}{
		{"nil tax", nil, &mockTaxStore{}, service.ErrTaxRequired},
		{"empty name", &model.Tax{Name: "  "}, &mockTaxStore{}, service.ErrInvalidName},
		{"short name", &model.Tax{Name: "A"}, &mockTaxStore{}, service.ErrInvalidNameMinLen},
		{"long name", &model.Tax{Name: strings.Repeat("a", 81)}, &mockTaxStore{}, service.ErrInvalidNameMaxLen},
		{"negative rate", &model.Tax{Name: "IVA", Rate: decimal.NewFromInt(-1)}, &mockTaxStore{}, service.ErrInvalidMinRate},
		{"rate > 100", &model.Tax{Name: "IVA", Rate: decimal.NewFromInt(101)}, &mockTaxStore{}, service.ErrInvalidMaxRate},
		{"valid tax", &model.Tax{Name: "IVA", Rate: decimal.NewFromInt(21)}, successStore, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewTaxService(tt.store)
			_, err := svc.CreateTax(ctx, tt.input)
			verifyError(t, tt.wantErr, err)
		})
	}
}

func TestTaxService_CreateTax(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		input   *model.Tax
		mock    *mockTaxStore
		wantErr error
		check   func(t *testing.T, tax *model.Tax)
	}{
		{
			name:  "error when tax name already exists",
			input: &model.Tax{Name: "IVA", Rate: decimal.RequireFromString("21.00")},
			mock: &mockTaxStore{
				createFn: func(ctx context.Context, tax *model.Tax) (*model.Tax, error) {
					return nil, store.ErrDuplicateTaxName
				},
			},
			wantErr: service.ErrTaxAlreadyExists,
		},
		{
			name:  "success creates tax and trims whitespace",
			input: &model.Tax{Name: " IVA ", Rate: decimal.RequireFromString("21.00")},
			mock: &mockTaxStore{
				createFn: func(ctx context.Context, tax *model.Tax) (*model.Tax, error) {
					if tax.Name != "IVA" {
						t.Errorf("expected trimmed name 'IVA', got '%s'", tax.Name)
					}
					return &model.Tax{ID: 1, Name: tax.Name, Rate: tax.Rate}, nil
				},
			},
			check: func(t *testing.T, tax *model.Tax) {
				if tax.ID == 0 {
					t.Error("expected valid tax ID, got 0")
				}
			},
		},
		{
			name:  "error propagated from store failure",
			input: &model.Tax{Name: "IVA", Rate: decimal.RequireFromString("21.00")},
			mock: &mockTaxStore{
				createFn: func(ctx context.Context, tax *model.Tax) (*model.Tax, error) {
					return nil, errStoreFailure
				},
			},
			wantErr: errStoreFailure,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewTaxService(tt.mock)
			created, err := svc.CreateTax(ctx, tt.input)

			if !verifyError(t, tt.wantErr, err) {
				return
			}
			if created == nil {
				t.Fatal("expected created tax, got nil")
			}
			if tt.check != nil {
				tt.check(t, created)
			}
		})
	}
}

func TestTaxService_GetAllTaxes(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		mock    *mockTaxStore
		wantErr error
		wantLen int
	}{
		{
			name: "success returns list of taxes",
			mock: &mockTaxStore{
				getAllFn: func(ctx context.Context) ([]*model.Tax, error) {
					return []*model.Tax{
						{ID: 1, Name: "IVA 21%", Rate: decimal.NewFromInt(21)},
						{ID: 2, Name: "IVA 10.5%", Rate: decimal.RequireFromString("10.5")},
					}, nil
				},
			},
			wantLen: 2,
		},
		{
			name: "error propagated from store",
			mock: &mockTaxStore{
				getAllFn: func(ctx context.Context) ([]*model.Tax, error) {
					return nil, errStoreFailure
				},
			},
			wantErr: errStoreFailure,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewTaxService(tt.mock)
			taxes, err := svc.GetAllTaxes(ctx)

			if !verifyError(t, tt.wantErr, err) {
				return
			}
			if len(taxes) != tt.wantLen {
				t.Errorf("expected %d taxes, got %d", tt.wantLen, len(taxes))
			}
		})
	}
}

func TestTaxService_GetTaxByID(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		input   int64
		mock    *mockTaxStore
		wantErr error
	}{
		{
			name:    "error when id is lower than 1",
			input:   0,
			mock:    &mockTaxStore{},
			wantErr: service.ErrInvalidID,
		},
		{
			name:  "error when tax is not found",
			input: 1,
			mock: &mockTaxStore{
				getByIDFn: func(ctx context.Context, id int64) (*model.Tax, error) {
					return nil, store.ErrNotFound
				},
			},
			wantErr: service.ErrTaxNotFound,
		},
		{
			name:  "success getting tax by id",
			input: 1,
			mock: &mockTaxStore{
				getByIDFn: func(ctx context.Context, id int64) (*model.Tax, error) {
					return &model.Tax{ID: 1, Name: "IVA 21%", Rate: decimal.NewFromInt(21)}, nil
				},
			},
		},
		{
			name:  "error propagated from store",
			input: 1,
			mock: &mockTaxStore{
				getByIDFn: func(ctx context.Context, id int64) (*model.Tax, error) {
					return nil, errStoreFailure
				},
			},
			wantErr: errStoreFailure,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewTaxService(tt.mock)
			tax, err := svc.GetTaxByID(ctx, tt.input)

			if !verifyError(t, tt.wantErr, err) {
				return
			}
			if tax == nil {
				t.Fatal("expected tax, got nil")
			}
			if tax.ID != tt.input {
				t.Errorf("expected tax ID %d, got %d", tt.input, tax.ID)
			}
		})
	}
}

func TestTaxService_UpdateTax(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		id      int64
		tax     *model.Tax
		mock    *mockTaxStore
		wantErr error
		check   func(t *testing.T, tax *model.Tax)
	}{
		{
			name:    "error when id is lower than 1",
			id:      0,
			tax:     &model.Tax{Name: "IVA", Rate: decimal.NewFromInt(21)},
			mock:    &mockTaxStore{},
			wantErr: service.ErrInvalidID,
		},
		{
			name: "error when tax is not found",
			id:   1,
			tax:  &model.Tax{Name: "IVA", Rate: decimal.NewFromInt(21)},
			mock: &mockTaxStore{
				updateFn: func(ctx context.Context, id int64, tax *model.Tax) (*model.Tax, error) {
					return nil, store.ErrNotFound
				},
			},
			wantErr: service.ErrTaxNotFound,
		},
		{
			name: "error when tax name already exists",
			id:   1,
			tax:  &model.Tax{Name: "IVA", Rate: decimal.NewFromInt(21)},
			mock: &mockTaxStore{
				updateFn: func(ctx context.Context, id int64, tax *model.Tax) (*model.Tax, error) {
					return nil, store.ErrDuplicateTaxName
				},
			},
			wantErr: service.ErrTaxAlreadyExists,
		},
		{
			name: "success updates tax and trims whitespace",
			id:   1,
			tax:  &model.Tax{Name: " IVA ", Rate: decimal.RequireFromString("21.00")},
			mock: &mockTaxStore{
				updateFn: func(ctx context.Context, id int64, tax *model.Tax) (*model.Tax, error) {
					if tax.Name != "IVA" {
						t.Errorf("expected trimmed name 'IVA', got '%s'", tax.Name)
					}
					return &model.Tax{ID: 1, Name: tax.Name, Rate: tax.Rate}, nil
				},
			},
			check: func(t *testing.T, tax *model.Tax) {
				if tax.ID == 0 {
					t.Error("expected valid tax ID, got 0")
				}
			},
		},
		{
			name: "error propagated from store failure",
			id:   1,
			tax:  &model.Tax{Name: "IVA", Rate: decimal.RequireFromString("21.00")},
			mock: &mockTaxStore{
				updateFn: func(ctx context.Context, id int64, tax *model.Tax) (*model.Tax, error) {
					return nil, errStoreFailure
				},
			},
			wantErr: errStoreFailure,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewTaxService(tt.mock)
			updated, err := svc.UpdateTax(ctx, tt.id, tt.tax)

			if !verifyError(t, tt.wantErr, err) {
				return
			}
			if updated == nil {
				t.Fatal("expected updated tax, got nil")
			}
			if updated.ID != tt.id {
				t.Errorf("expected tax ID %d, got %d", tt.id, updated.ID)
			}
			if tt.check != nil {
				tt.check(t, updated)
			}
		})
	}
}

func TestTaxService_DeleteTax(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		input   int64
		mock    *mockTaxStore
		wantErr error
	}{
		{
			name:    "error when id is lower than 1",
			input:   0,
			mock:    &mockTaxStore{},
			wantErr: service.ErrInvalidID,
		},
		{
			name:  "error when tax not found",
			input: 1,
			mock: &mockTaxStore{
				deleteFn: func(ctx context.Context, id int64) error {
					return store.ErrNotFound
				},
			},
			wantErr: service.ErrTaxNotFound,
		},
		{
			name:  "error propagated from store",
			input: 1,
			mock: &mockTaxStore{
				deleteFn: func(ctx context.Context, id int64) error {
					return errStoreFailure
				},
			},
			wantErr: errStoreFailure,
		},
		{
			name:  "success",
			input: 1,
			mock: &mockTaxStore{
				deleteFn: func(ctx context.Context, id int64) error {
					return nil
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewTaxService(tt.mock)
			err := svc.DeleteTax(ctx, tt.input)
			verifyError(t, tt.wantErr, err)
		})
	}
}

func TestTaxService_SearchByName(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		query   string
		mock    *mockTaxStore
		wantErr error
		wantLen int
	}{
		{
			name:    "empty query returns empty list",
			query:   "",
			mock:    &mockTaxStore{},
			wantLen: 0,
		},
		{
			name:    "whitespace query returns empty list",
			query:   "  ",
			mock:    &mockTaxStore{},
			wantLen: 0,
		},
		{
			name:  "success returns matching taxes",
			query: "IVA",
			mock: &mockTaxStore{
				searchByNameFn: func(ctx context.Context, q string) ([]*model.Tax, error) {
					return []*model.Tax{
						{ID: 1, Name: "IVA 21%", Rate: decimal.NewFromInt(21)},
					}, nil
				},
			},
			wantLen: 1,
		},
		{
			name:  "error propagated from store",
			query: "IVA",
			mock: &mockTaxStore{
				searchByNameFn: func(ctx context.Context, q string) ([]*model.Tax, error) {
					return nil, errStoreFailure
				},
			},
			wantErr: errStoreFailure,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewTaxService(tt.mock)
			taxes, err := svc.SearchByName(ctx, tt.query)

			if !verifyError(t, tt.wantErr, err) {
				return
			}
			if len(taxes) != tt.wantLen {
				t.Errorf("expected %d taxes, got %d", tt.wantLen, len(taxes))
			}
		})
	}
}

// test-local sentinel for store failure propagation tests.
var errStoreFailure = errors.New("store failure")