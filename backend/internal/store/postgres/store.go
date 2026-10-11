package postgres

import "context"

type Store[T any] interface {
	Create(ctx context.Context, entity *T) (*T, error)
	GetAll(ctx context.Context) ([]*T, error)
	GetByID(ctx context.Context, id int64) (*T, error)
	SearchByName(ctx context.Context, query string) ([]*T, error)
	Update(ctx context.Context, id int64, entity *T) (*T, error)
	Delete(ctx context.Context, id int64) error
}