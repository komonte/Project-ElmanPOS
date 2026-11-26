package model

import (
	"testing"
	"time"
)

func TestPriceStateAt(t *testing.T) {
	received := time.Date(2026, 11, 26, 15, 0, 0, 0, time.UTC)
	before, after := received.Add(-time.Hour), received.Add(time.Minute)
	for _, c := range []struct {
		from *time.Time
		want PriceState
	}{{nil, PriceMissing}, {&before, PriceOld}, {&received, PriceOK}, {&after, PriceOK}} {
		if got := PriceStateAt(c.from, received); got != c.want {
			t.Errorf("%v: got %s, want %s", c.from, got, c.want)
		}
	}
}
