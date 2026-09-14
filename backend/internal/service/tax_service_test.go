package service_test

import (
	"context"
	"errors"
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
	updateFn       func(ctx context.Context, tax *model.Tax) (*model.Tax, error)
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

func (m *mockTaxStore) Update(ctx context.Context, tax *model.Tax) (*model.Tax, error) {
	return m.updateFn(ctx, tax)
}

func (m *mockTaxStore) Delete(ctx context.Context, id int64) error {
	return m.deleteFn(ctx, id)
}

var errStore = errors.New("store failure")

func TestTaxService_CreateTax(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		input   *model.Tax
		mock    *mockTaxStore
		wantErr error
	}{
		{"nil tax", nil, &mockTaxStore{}, model.ErrNilTax},
		{"empty name", &model.Tax{Name: "  "}, &mockTaxStore{}, model.ErrEmptyTaxName},
		{"short name", &model.Tax{Name: "A"}, &mockTaxStore{}, model.ErrTaxNameTooShort},
		{"long name", &model.Tax{Name: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, &mockTaxStore{}, model.ErrTaxNameTooLong},
		{"negative rate", &model.Tax{Name: "IVA", Rate: decimal.NewFromInt(-1)}, &mockTaxStore{}, model.ErrInvalidRate},
		{"rate over 100", &model.Tax{Name: "IVA", Rate: decimal.NewFromInt(101)}, &mockTaxStore{}, model.ErrInvalidRate},
		{"duplicate name", &model.Tax{Name: "IVA", Rate: decimal.NewFromInt(21)}, &mockTaxStore{
			createFn: func(_ context.Context, _ *model.Tax) (*model.Tax, error) {
				return nil, store.ErrDuplicateTaxName
			},
		}, service.ErrTaxAlreadyExists},
		{"store failure", &model.Tax{Name: "IVA", Rate: decimal.NewFromInt(21)}, &mockTaxStore{
			createFn: func(_ context.Context, _ *model.Tax) (*model.Tax, error) {
				return nil, errStore
			},
		}, errStore},
		{"success", &model.Tax{Name: "IVA", Rate: decimal.NewFromInt(21)}, &mockTaxStore{
			createFn: func(_ context.Context, tax *model.Tax) (*model.Tax, error) {
				return &model.Tax{ID: 1, Name: tax.Name, Rate: tax.Rate}, nil
			},
		}, nil},
		{"trims whitespace", &model.Tax{Name: " IVA ", Rate: decimal.NewFromInt(21)}, &mockTaxStore{
			createFn: func(_ context.Context, tax *model.Tax) (*model.Tax, error) {
				if tax.Name != "IVA" {
					t.Errorf("expected 'IVA', got %q", tax.Name)
				}
				return &model.Tax{ID: 1, Name: tax.Name, Rate: tax.Rate}, nil
			},
		}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewTaxService(tt.mock)
			created, err := svc.CreateTax(ctx, tt.input)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if created == nil || created.ID == 0 {
				t.Fatal("expected valid tax with ID")
			}
		})
	}
}

func TestTaxService_GetAllTaxes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		mock    *mockTaxStore
		wantErr error
		wantLen int
	}{
		{"success", &mockTaxStore{
			getAllFn: func(_ context.Context) ([]*model.Tax, error) {
				return []*model.Tax{
					{ID: 1, Name: "IVA 21%", Rate: decimal.NewFromInt(21)},
					{ID: 2, Name: "IVA 10.5%", Rate: decimal.RequireFromString("10.5")},
				}, nil
			},
		}, nil, 2},
		{"store failure", &mockTaxStore{
			getAllFn: func(_ context.Context) ([]*model.Tax, error) {
				return nil, errStore
			},
		}, errStore, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewTaxService(tt.mock)
			taxes, err := svc.GetAllTaxes(ctx)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if len(taxes) != tt.wantLen {
				t.Errorf("len: got %d, want %d", len(taxes), tt.wantLen)
			}
		})
	}
}

func TestTaxService_GetTaxByID(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		id      int64
		mock    *mockTaxStore
		wantErr error
	}{
		{"invalid id", 0, &mockTaxStore{}, service.ErrInvalidID},
		{"not found", 1, &mockTaxStore{
			getByIDFn: func(_ context.Context, _ int64) (*model.Tax, error) {
				return nil, store.ErrNotFound
			},
		}, service.ErrTaxNotFound},
		{"store failure", 1, &mockTaxStore{
			getByIDFn: func(_ context.Context, _ int64) (*model.Tax, error) {
				return nil, errStore
			},
		}, errStore},
		{"success", 1, &mockTaxStore{
			getByIDFn: func(_ context.Context, id int64) (*model.Tax, error) {
				return &model.Tax{ID: id, Name: "IVA 21%", Rate: decimal.NewFromInt(21)}, nil
			},
		}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewTaxService(tt.mock)
			tax, err := svc.GetTaxByID(ctx, tt.id)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if tax.ID != tt.id {
				t.Errorf("id: got %d, want %d", tax.ID, tt.id)
			}
		})
	}
}

func TestTaxService_UpdateTax(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		tax     *model.Tax
		mock    *mockTaxStore
		wantErr error
	}{
		{"invalid id", &model.Tax{ID: 0}, &mockTaxStore{}, service.ErrInvalidID},
		{"empty name", &model.Tax{ID: 1, Name: "  "}, &mockTaxStore{}, model.ErrEmptyTaxName},
		{"not found", &model.Tax{ID: 1, Name: "IVA", Rate: decimal.NewFromInt(21)}, &mockTaxStore{
			updateFn: func(_ context.Context, _ *model.Tax) (*model.Tax, error) {
				return nil, store.ErrNotFound
			},
		}, service.ErrTaxNotFound},
		{"duplicate name", &model.Tax{ID: 1, Name: "IVA", Rate: decimal.NewFromInt(21)}, &mockTaxStore{
			updateFn: func(_ context.Context, _ *model.Tax) (*model.Tax, error) {
				return nil, store.ErrDuplicateTaxName
			},
		}, service.ErrTaxAlreadyExists},
		{"store failure", &model.Tax{ID: 1, Name: "IVA", Rate: decimal.NewFromInt(21)}, &mockTaxStore{
			updateFn: func(_ context.Context, _ *model.Tax) (*model.Tax, error) {
				return nil, errStore
			},
		}, errStore},
		{"success", &model.Tax{ID: 1, Name: "IVA", Rate: decimal.NewFromInt(21)}, &mockTaxStore{
			updateFn: func(_ context.Context, tax *model.Tax) (*model.Tax, error) {
				return &model.Tax{ID: tax.ID, Name: tax.Name, Rate: tax.Rate}, nil
			},
		}, nil},
		{"trims whitespace", &model.Tax{ID: 1, Name: " IVA ", Rate: decimal.NewFromInt(21)}, &mockTaxStore{
			updateFn: func(_ context.Context, tax *model.Tax) (*model.Tax, error) {
				if tax.Name != "IVA" {
					t.Errorf("expected 'IVA', got %q", tax.Name)
				}
				return &model.Tax{ID: tax.ID, Name: tax.Name, Rate: tax.Rate}, nil
			},
		}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewTaxService(tt.mock)
			updated, err := svc.UpdateTax(ctx, tt.tax)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}
			if updated == nil || updated.ID == 0 {
				t.Fatal("expected valid tax with ID")
			}
		})
	}
}

func TestTaxService_DeleteTax(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		id      int64
		mock    *mockTaxStore
		wantErr error
	}{
		{"invalid id", 0, &mockTaxStore{}, service.ErrInvalidID},
		{"not found", 1, &mockTaxStore{
			deleteFn: func(_ context.Context, _ int64) error { return store.ErrNotFound },
		}, service.ErrTaxNotFound},
		{"store failure", 1, &mockTaxStore{
			deleteFn: func(_ context.Context, _ int64) error { return errStore },
		}, errStore},
		{"success", 1, &mockTaxStore{
			deleteFn: func(_ context.Context, _ int64) error { return nil },
		}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewTaxService(tt.mock)
			err := svc.DeleteTax(ctx, tt.id)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestTaxService_SearchByName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name    string
		query   string
		mock    *mockTaxStore
		wantErr error
		wantLen int
	}{
		{"empty query", "", &mockTaxStore{}, nil, 0},
		{"whitespace query", "  ", &mockTaxStore{}, nil, 0},
		{"success", "IVA", &mockTaxStore{
			searchByNameFn: func(_ context.Context, _ string) ([]*model.Tax, error) {
				return []*model.Tax{{ID: 1, Name: "IVA 21%", Rate: decimal.NewFromInt(21)}}, nil
			},
		}, nil, 1},
		{"store failure", "IVA", &mockTaxStore{
			searchByNameFn: func(_ context.Context, _ string) ([]*model.Tax, error) {
				return nil, errStore
			},
		}, errStore, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := service.NewTaxService(tt.mock)
			taxes, err := svc.SearchByName(ctx, tt.query)

			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("error: got %v, want %v", err, tt.wantErr)
			}
			if len(taxes) != tt.wantLen {
				t.Errorf("len: got %d, want %d", len(taxes), tt.wantLen)
			}
		})
	}
}
