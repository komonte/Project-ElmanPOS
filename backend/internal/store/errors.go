package store

import "errors"

var (
	ErrNotFound      = errors.New("resource not found")
	ErrDuplicateName = errors.New("name already exists")
	ErrInUse         = errors.New("resource in use by other records")

	// TODO(suppliers): service.go solo traduce ErrDuplicateName a ErrAlreadyExists;
	// este error llega sin traducir (API responde 400 en vez de 409).
	ErrDuplicateTaxID = errors.New("tax id already exists")
)
