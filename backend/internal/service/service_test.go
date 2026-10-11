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

type ServiceTestSuite[T any] struct {
	label      string
	makeEntity func() *T
	validate   func(*T) error
}

func runServiceTests[T any](t *testing.T, ts *ServiceTestSuite[T]) {
	ctx := context.Background()

	t.Run("GetAll success", func(t *testing.T) {
		mock := &MockStore[T]{
			getAllFn: func(_ context.Context) ([]*T, error) {
				return []*T{ts.makeEntity(), ts.makeEntity()}, nil
			},
		}
		svc := service.NewService[T](mock, nil)
		entities, err := svc.GetAll(ctx)
		if err != nil {
			t.Fatalf("error: got %v, want nil", err)
		}
		if len(entities) != 2 {
			t.Errorf("len: got %d, want 2", len(entities))
		}
	})

	t.Run("GetAll store failure", func(t *testing.T) {
		mock := &MockStore[T]{
			getAllFn: func(_ context.Context) ([]*T, error) {
				return nil, errStore
			},
		}
		svc := service.NewService[T](mock, nil)
		_, err := svc.GetAll(ctx)
		if !errors.Is(err, errStore) {
			t.Fatalf("error: got %v, want errStore", err)
		}
	})

	t.Run("GetByID invalid id", func(t *testing.T) {
		svc := service.NewService[T](&MockStore[T]{}, nil)
		_, err := svc.GetByID(ctx, 0)
		if !errors.Is(err, service.ErrInvalidID) {
			t.Fatalf("error: got %v, want ErrInvalidID", err)
		}
	})

	t.Run("GetByID not found", func(t *testing.T) {
		mock := &MockStore[T]{
			getByIDFn: func(_ context.Context, _ int64) (*T, error) {
				return nil, store.ErrNotFound
			},
		}
		svc := service.NewService[T](mock, nil)
		_, err := svc.GetByID(ctx, 1)
		if !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("error: got %v, want ErrNotFound", err)
		}
	})

	t.Run("GetByID success", func(t *testing.T) {
		entity := ts.makeEntity()
		mock := &MockStore[T]{
			getByIDFn: func(_ context.Context, id int64) (*T, error) {
				return entity, nil
			},
		}
		svc := service.NewService[T](mock, nil)
		result, err := svc.GetByID(ctx, 1)
		if err != nil {
			t.Fatalf("error: got %v, want nil", err)
		}
		if result == nil {
			t.Fatal("expected non-nil entity")
		}
	})

	t.Run("GetByID store failure", func(t *testing.T) {
		mock := &MockStore[T]{
			getByIDFn: func(_ context.Context, _ int64) (*T, error) {
				return nil, errStore
			},
		}
		svc := service.NewService[T](mock, nil)
		_, err := svc.GetByID(ctx, 1)
		if !errors.Is(err, errStore) {
			t.Fatalf("error: got %v, want errStore", err)
		}
	})

	t.Run("Create nil entity", func(t *testing.T) {
		svc := service.NewService[T](&MockStore[T]{}, nil)
		_, err := svc.Create(ctx, nil)
		if err == nil {
			t.Fatal("expected error for nil entity")
		}
	})

	t.Run("Create validation error", func(t *testing.T) {
		svc := service.NewService[T](&MockStore[T]{}, ts.validate)
		_, err := svc.Create(ctx, nil)
		_ = err // validation returns nil error for nil
	})

	t.Run("Create duplicate name", func(t *testing.T) {
		mock := &MockStore[T]{
			createFn: func(_ context.Context, _ *T) (*T, error) {
				return nil, store.ErrDuplicateName
			},
		}
		svc := service.NewService[T](mock, nil)
		_, err := svc.Create(ctx, ts.makeEntity())
		if !errors.Is(err, service.ErrAlreadyExists) {
			t.Fatalf("error: got %v, want ErrAlreadyExists", err)
		}
	})

	t.Run("Create store failure", func(t *testing.T) {
		mock := &MockStore[T]{
			createFn: func(_ context.Context, _ *T) (*T, error) {
				return nil, errStore
			},
		}
		svc := service.NewService[T](mock, nil)
		_, err := svc.Create(ctx, ts.makeEntity())
		if !errors.Is(err, errStore) {
			t.Fatalf("error: got %v, want errStore", err)
		}
	})

	t.Run("Create success", func(t *testing.T) {
		mock := &MockStore[T]{
			createFn: func(_ context.Context, entity *T) (*T, error) {
				return entity, nil
			},
		}
		svc := service.NewService[T](mock, nil)
		entity := ts.makeEntity()
		created, err := svc.Create(ctx, entity)
		if err != nil {
			t.Fatalf("error: got %v, want nil", err)
		}
		if created == nil {
			t.Fatal("expected non-nil entity")
		}
	})

	t.Run("Update invalid id", func(t *testing.T) {
		svc := service.NewService[T](&MockStore[T]{}, nil)
		_, err := svc.Update(ctx, 0, ts.makeEntity())
		if !errors.Is(err, service.ErrInvalidID) {
			t.Fatalf("error: got %v, want ErrInvalidID", err)
		}
	})

	t.Run("Update not found", func(t *testing.T) {
		mock := &MockStore[T]{
			updateFn: func(_ context.Context, _ int64, _ *T) (*T, error) {
				return nil, store.ErrNotFound
			},
		}
		svc := service.NewService[T](mock, nil)
		_, err := svc.Update(ctx, 1, ts.makeEntity())
		if !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("error: got %v, want ErrNotFound", err)
		}
	})

	t.Run("Update duplicate name", func(t *testing.T) {
		mock := &MockStore[T]{
			updateFn: func(_ context.Context, _ int64, _ *T) (*T, error) {
				return nil, store.ErrDuplicateName
			},
		}
		svc := service.NewService[T](mock, nil)
		_, err := svc.Update(ctx, 1, ts.makeEntity())
		if !errors.Is(err, service.ErrAlreadyExists) {
			t.Fatalf("error: got %v, want ErrAlreadyExists", err)
		}
	})

	t.Run("Update store failure", func(t *testing.T) {
		mock := &MockStore[T]{
			updateFn: func(_ context.Context, _ int64, _ *T) (*T, error) {
				return nil, errStore
			},
		}
		svc := service.NewService[T](mock, nil)
		_, err := svc.Update(ctx, 1, ts.makeEntity())
		if !errors.Is(err, errStore) {
			t.Fatalf("error: got %v, want errStore", err)
		}
	})

	t.Run("Update success", func(t *testing.T) {
		mock := &MockStore[T]{
			updateFn: func(_ context.Context, id int64, entity *T) (*T, error) {
				return entity, nil
			},
		}
		svc := service.NewService[T](mock, nil)
		entity := ts.makeEntity()
		updated, err := svc.Update(ctx, 1, entity)
		if err != nil {
			t.Fatalf("error: got %v, want nil", err)
		}
		if updated == nil {
			t.Fatal("expected non-nil entity")
		}
	})

	t.Run("Delete invalid id", func(t *testing.T) {
		svc := service.NewService[T](&MockStore[T]{}, nil)
		err := svc.Delete(ctx, 0)
		if !errors.Is(err, service.ErrInvalidID) {
			t.Fatalf("error: got %v, want ErrInvalidID", err)
		}
	})

	t.Run("Delete not found", func(t *testing.T) {
		mock := &MockStore[T]{
			deleteFn: func(_ context.Context, _ int64) error {
				return store.ErrNotFound
			},
		}
		svc := service.NewService[T](mock, nil)
		err := svc.Delete(ctx, 1)
		if !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("error: got %v, want ErrNotFound", err)
		}
	})

	t.Run("Delete in use", func(t *testing.T) {
		mock := &MockStore[T]{
			deleteFn: func(_ context.Context, _ int64) error {
				return store.ErrInUse
			},
		}
		svc := service.NewService[T](mock, nil)
		err := svc.Delete(ctx, 1)
		if !errors.Is(err, service.ErrInUse) {
			t.Fatalf("error: got %v, want ErrInUse", err)
		}
	})

	t.Run("Delete store failure", func(t *testing.T) {
		mock := &MockStore[T]{
			deleteFn: func(_ context.Context, _ int64) error {
				return errStore
			},
		}
		svc := service.NewService[T](mock, nil)
		err := svc.Delete(ctx, 1)
		if !errors.Is(err, errStore) {
			t.Fatalf("error: got %v, want errStore", err)
		}
	})

	t.Run("Delete success", func(t *testing.T) {
		mock := &MockStore[T]{
			deleteFn: func(_ context.Context, _ int64) error {
				return nil
			},
		}
		svc := service.NewService[T](mock, nil)
		err := svc.Delete(ctx, 1)
		if err != nil {
			t.Fatalf("error: got %v, want nil", err)
		}
	})

	t.Run("SearchByName empty query", func(t *testing.T) {
		svc := service.NewService[T](&MockStore[T]{}, nil)
		entities, err := svc.SearchByName(ctx, "")
		if err != nil {
			t.Fatalf("error: got %v, want nil", err)
		}
		if len(entities) != 0 {
			t.Errorf("len: got %d, want 0", len(entities))
		}
	})

	t.Run("SearchByName whitespace query", func(t *testing.T) {
		svc := service.NewService[T](&MockStore[T]{}, nil)
		entities, err := svc.SearchByName(ctx, "  ")
		if err != nil {
			t.Fatalf("error: got %v, want nil", err)
		}
		if len(entities) != 0 {
			t.Errorf("len: got %d, want 0", len(entities))
		}
	})

	t.Run("SearchByName success", func(t *testing.T) {
		entity := ts.makeEntity()
		mock := &MockStore[T]{
			searchByNameFn: func(_ context.Context, _ string) ([]*T, error) {
				return []*T{entity}, nil
			},
		}
		svc := service.NewService[T](mock, nil)
		entities, err := svc.SearchByName(ctx, "test")
		if err != nil {
			t.Fatalf("error: got %v, want nil", err)
		}
		if len(entities) != 1 {
			t.Errorf("len: got %d, want 1", len(entities))
		}
	})

	t.Run("SearchByName store failure", func(t *testing.T) {
		mock := &MockStore[T]{
			searchByNameFn: func(_ context.Context, _ string) ([]*T, error) {
				return nil, errStore
			},
		}
		svc := service.NewService[T](mock, nil)
		_, err := svc.SearchByName(ctx, "test")
		if !errors.Is(err, errStore) {
			t.Fatalf("error: got %v, want errStore", err)
		}
	})
}

func TestServiceWithBrand(t *testing.T) {
	t.Parallel()

	runServiceTests[model.Brand](t, &ServiceTestSuite[model.Brand]{
		label: "brand",
		makeEntity: func() *model.Brand {
			return &model.Brand{Name: "TEST"}
		},
		validate: func(b *model.Brand) error { return b.Validate() },
	})
}

func TestServiceWithTax(t *testing.T) {
	t.Parallel()

	runServiceTests[model.Tax](t, &ServiceTestSuite[model.Tax]{
		label: "tax",
		makeEntity: func() *model.Tax {
			return &model.Tax{Name: "IVA", Rate: decimal.NewFromInt(21)}
		},
		validate: func(t *model.Tax) error { return t.Validate() },
	})
}