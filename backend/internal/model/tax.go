package model

import (
	"github.com/shopspring/decimal"
)

type Tax struct {
	ID int64 `json:"id" db:"id"`
	Name string `json:"name" db:"name"`
	Rate decimal.Decimal `json:"rate" db:"rate"`
}


