package service

import (
	"context"
	"errors"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

type Service[T any] struct {
	store    postgres.Store[T]
	validate func(*T) error
}

func NewService[T any](store postgres.Store[T], validate func(*T) error) *Service[T] {
	return &Service[T]{
		store:    store,
		validate: validate,
	}
}

func (s *Service[T]) GetAll(ctx context.Context) ([]*T, error) {
	return s.store.GetAll(ctx)
}

func (s *Service[T]) GetByID(ctx context.Context, id int64) (*T, error) {
	if id <= 0 {
		return nil, ErrInvalidID
	}
	entity, err := s.store.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return entity, nil
}

func (s *Service[T]) Create(ctx context.Context, entity *T) (*T, error) {
	if entity == nil {
		return nil, errors.New("entity cannot be nil")
	}
	if s.validate != nil {
		if err := s.validate(entity); err != nil {
			return nil, err
		}
	}
	created, err := s.store.Create(ctx, entity)
	if err != nil {
		if errors.Is(err, store.ErrDuplicateName) {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	return created, nil
}

func (s *Service[T]) Update(ctx context.Context, id int64, entity *T) (*T, error) {
	if id <= 0 {
		return nil, ErrInvalidID
	}
	if entity == nil {
		return nil, errors.New("entity cannot be nil")
	}
	if s.validate != nil {
		if err := s.validate(entity); err != nil {
			return nil, err
		}
	}
	updated, err := s.store.Update(ctx, id, entity)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrNotFound
		}
		if errors.Is(err, store.ErrDuplicateName) {
			return nil, ErrAlreadyExists
		}
		return nil, err
	}
	return updated, nil
}

func (s *Service[T]) Delete(ctx context.Context, id int64) error {
	if id <= 0 {
		return ErrInvalidID
	}
	err := s.store.Delete(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrNotFound
		}
		if errors.Is(err, store.ErrInUse) {
			return ErrInUse
		}
		return err
	}
	return nil
}

func (s *Service[T]) SearchByName(ctx context.Context, query string) ([]*T, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return make([]*T, 0), nil
	}
	return s.store.SearchByName(ctx, q)
}