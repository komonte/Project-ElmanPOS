package postgres

import (
	"database/sql"
	"errors"

	"github.com/lib/pq"
)

const (
	errCodeUniqueViolation = "23505"
	errCodeForeignKeyViolation = "23503"
)

type DB struct {
	*sql.DB
}

func isUniqueViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == errCodeUniqueViolation
	}
	return false
}

func isForeignKeyViolation(err error) bool {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == errCodeForeignKeyViolation
	}
	return false
}
