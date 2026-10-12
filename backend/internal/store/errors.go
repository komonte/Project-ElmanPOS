package store

import "errors"

var (
	ErrNotFound      = errors.New("resource not found")
	ErrDuplicateName = errors.New("name already exists")
	ErrInUse         = errors.New("resource in use by other records")
)