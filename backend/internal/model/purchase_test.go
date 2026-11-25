package model_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

func item(productID int64, qty, cost string) *model.PurchaseItem {
	return &model.PurchaseItem{ProductID: productID, Quantity: dec(qty), UnitCost: dec(cost)}
}

func validPurchase() *model.Purchase {
	return &model.Purchase{
		SupplierID:    1,
		VoucherNumber: "  A-0001-00001234 ",
		Total:         dec("1150"),
		Items:         []*model.PurchaseItem{item(1, "10", "100"), item(2, "2.5", "60")},
	}
}

func TestPurchaseValidate(t *testing.T) {
	p := validPurchase()
	if err := p.Validate(); err != nil {
		t.Fatalf("valid purchase: %v", err)
	}
	if p.VoucherNumber != "A-0001-00001234" {
		t.Errorf("voucher not trimmed: %q", p.VoucherNumber)
	}

	tests := []struct {
		name    string
		change  func(p *model.Purchase)
		wantErr error
	}{
		{"no supplier", func(p *model.Purchase) { p.SupplierID = 0 }, model.ErrPurchaseSupplierMissing},
		{"voucher too long", func(p *model.Purchase) { p.VoucherNumber = strings.Repeat("9", 61) }, model.ErrPurchaseVoucherTooLong},
		{"empty voucher is fine", func(p *model.Purchase) { p.VoucherNumber = "" }, nil},
		{"total 0 is fine", func(p *model.Purchase) { p.Total = dec("0") }, nil},
		{"negative total", func(p *model.Purchase) { p.Total = dec("-1") }, model.ErrInvalidPurchaseTotal},
		{"total with 3 decimals", func(p *model.Purchase) { p.Total = dec("10.555") }, model.ErrInvalidPurchaseTotal},
		{"total too large", func(p *model.Purchase) { p.Total = dec("10000000000") }, model.ErrInvalidPurchaseTotal},
		{"no lines", func(p *model.Purchase) { p.Items = nil }, model.ErrPurchaseNoItems},
		{"nil line", func(p *model.Purchase) { p.Items[1] = nil }, model.ErrNilPurchaseItem},
		{"line without product", func(p *model.Purchase) { p.Items[0].ProductID = 0 }, model.ErrPurchaseItemProduct},
		{"zero quantity", func(p *model.Purchase) { p.Items[0].Quantity = dec("0") }, model.ErrInvalidPurchaseQuantity},
		{"quantity with 4 decimals", func(p *model.Purchase) { p.Items[0].Quantity = dec("1.0001") }, model.ErrInvalidPurchaseQuantity},
		{"zero cost", func(p *model.Purchase) { p.Items[0].UnitCost = dec("0") }, model.ErrInvalidCost},
		{"cost with 3 decimals", func(p *model.Purchase) { p.Items[0].UnitCost = dec("1.005") }, model.ErrInvalidCost},
		{"batch and expiry", func(p *model.Purchase) {
			p.Items[0].BatchNumber, p.Items[0].ExpiryDate = " L-2475 ", "2027-03-31"
		}, nil},
		{"batch without expiry (non-perishable)", func(p *model.Purchase) { p.Items[0].BatchNumber = "L-1" }, nil},
		{"batch too long", func(p *model.Purchase) { p.Items[0].BatchNumber = strings.Repeat("L", 81) }, model.ErrBatchNumberTooLong},
		{"expiry without batch", func(p *model.Purchase) { p.Items[0].ExpiryDate = "2027-03-31" }, model.ErrExpiryWithoutBatch},
		{"bad expiry format", func(p *model.Purchase) {
			p.Items[0].BatchNumber, p.Items[0].ExpiryDate = "L-1", "31/03/2027"
		}, model.ErrInvalidExpiryDate},
		{"impossible expiry date", func(p *model.Purchase) {
			p.Items[0].BatchNumber, p.Items[0].ExpiryDate = "L-1", "2027-02-30"
		}, model.ErrInvalidExpiryDate},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validPurchase()
			tt.change(p)
			err := p.Validate()
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("got %v, want no error", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want %v", err, tt.wantErr)
			}
		})
	}
}

// El error de un renglón dice cuál, contando desde 1.
func TestPurchaseValidateNamesTheLine(t *testing.T) {
	p := validPurchase()
	p.Items[1].Quantity = dec("0")
	err := p.Validate()
	if err == nil || !strings.HasPrefix(err.Error(), "line 2: ") {
		t.Errorf("got %v, want it to start with \"line 2: \"", err)
	}
}

func TestPurchaseItemTrimsBatch(t *testing.T) {
	it := item(1, "1", "1")
	it.BatchNumber, it.ExpiryDate = "  L-2475 ", " 2027-03-31 "
	if err := it.Validate(); err != nil {
		t.Fatal(err)
	}
	if it.BatchNumber != "L-2475" || it.ExpiryDate != "2027-03-31" {
		t.Errorf("got %q %q", it.BatchNumber, it.ExpiryDate)
	}
}

func TestPurchaseValidateNil(t *testing.T) {
	var p *model.Purchase
	if err := p.Validate(); !errors.Is(err, model.ErrNilPurchase) {
		t.Errorf("got %v, want %v", err, model.ErrNilPurchase)
	}
}

func TestPurchaseTotals(t *testing.T) {
	tests := []struct {
		name      string
		total     string
		items     []*model.PurchaseItem
		wantLines string
		wantDiff  string
	}{
		// 10 × 100 + 2,5 × 60 = 1150.
		{"matches", "1150", []*model.PurchaseItem{item(1, "10", "100"), item(2, "2.5", "60")}, "1150", "0"},
		{"invoice says more", "1200", []*model.PurchaseItem{item(1, "10", "100"), item(2, "2.5", "60")}, "1150", "50"},
		{"invoice says less", "1100", []*model.PurchaseItem{item(1, "10", "100"), item(2, "2.5", "60")}, "1150", "-50"},
		// Cada renglón se redondea: 0,333 kg × 99,99 = 33,29667 → 33,30.
		{"line rounded to cents", "66.60", []*model.PurchaseItem{item(1, "0.333", "99.99"), item(1, "0.333", "99.99")}, "66.60", "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &model.Purchase{Total: dec(tt.total), Items: tt.items}
			if got := p.LinesTotal(); !got.Equal(dec(tt.wantLines)) {
				t.Errorf("lines total: got %s, want %s", got, tt.wantLines)
			}
			if got := p.TotalDifference(); !got.Equal(dec(tt.wantDiff)) {
				t.Errorf("difference: got %s, want %s", got, tt.wantDiff)
			}
		})
	}
}
