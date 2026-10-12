package service

import "errors"

var (
	ErrInvalidID    = errors.New("invalid id")
	ErrNotFound     = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrInUse        = errors.New("in use by other records")
)