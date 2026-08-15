// Package model defines domain entities, data structures, and types for ElmanPOS.
package model

type Brand struct {
	ID int64 `json:"id" db:"id"`
	Name string `json:"name" db:"name"`
}
