package purchases

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
)

// Precios por factura: una compra recibida de verdad y el estado de sus
// precios a medida que se ponen.
func TestInvoicePrices(t *testing.T) {
	f := newFixture(t)
	res := f.receive(t, "1500", line(f.plain, "10", "100"), batchLine(f.batched, "2", "250", "L-1", "2031-01-01"))
	id := res.Purchase.ID

	inv, err := f.svc.Invoice(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Summary.ID != id || inv.Summary.Products != 2 || inv.Summary.Unpriced != 2 ||
		inv.Summary.ReceivedByName == nil || *inv.Summary.ReceivedByName != "Usuario Test" {
		t.Errorf("summary: %+v", inv.Summary)
	}
	if len(inv.Products) != 2 || inv.Products[0].ProductID != f.plain || inv.Products[0].State != model.PriceMissing {
		t.Fatalf("products: %+v", inv.Products)
	}
	// Margen 30% e IVA 21%: 100 × 1,21 / 0,7 = 172,86 (model.SuggestPrice).
	plain := inv.Products[0]
	want, _ := model.SuggestPrice(dec("100"), dec("30"), dec("21"))
	if plain.Suggested == nil || !plain.Suggested.Equal(want) || !plain.Quantity.Equal(dec("10")) || !plain.UnitCost.Equal(dec("100")) {
		t.Errorf("plain: %+v (want suggested %v)", plain, want)
	}

	// Un precio de antes de la factura: "anterior"; uno de después: cotizado.
	f.exec(t, `INSERT INTO prices (product_id, unit_price, valid_from) VALUES ($1, 150, now() - interval '1 day');`, f.plain)
	f.exec(t, `INSERT INTO prices (product_id, unit_price, valid_from) VALUES ($1, 600, now() + interval '1 second');`, f.batched)
	inv, err = f.svc.Invoice(f.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Products[0].State != model.PriceOld || inv.Products[1].State != model.PriceOK || inv.Summary.Unpriced != 1 {
		t.Errorf("after prices: %s, %s, unpriced %d", inv.Products[0].State, inv.Products[1].State, inv.Summary.Unpriced)
	}

	list, err := f.svc.List(f.ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range list {
		found = found || p.ID == id
	}
	if !found {
		t.Error("the purchase must be in the unpriced list")
	}

	if _, err := f.svc.Invoice(f.ctx, 0); !errors.Is(err, service.ErrInvalidID) {
		t.Errorf("id 0: %v", err)
	}
	if _, err := f.svc.Invoice(f.ctx, 1<<40); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
}

func TestMissingPricesAndPending(t *testing.T) {
	f := newFixture(t)
	f.receive(t, "1000", line(f.plain, "10", "100"))
	missing, err := f.svc.MissingPrices(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	var row *PricedRow
	for i := range missing {
		if missing[i].ProductID == f.plain {
			row = &missing[i]
		}
	}
	if row == nil || row.State != model.PriceMissing || !row.Quantity.Equal(dec("10")) || row.Suggested == nil {
		t.Fatalf("missing: %+v", row)
	}
	p, err := f.svc.Pending(f.ctx)
	if err != nil || p.MissingPrices < 1 || p.UnpricedPurchases < 1 {
		t.Errorf("pending: %+v, %v", p, err)
	}

	// Con precio, sale de la lista.
	f.exec(t, `INSERT INTO prices (product_id, unit_price, valid_from) VALUES ($1, 200, $2);`, f.plain, time.Now())
	missing, _ = f.svc.MissingPrices(f.ctx)
	for _, m := range missing {
		if m.ProductID == f.plain {
			t.Error("a product with a price is not missing")
		}
	}
}

// Un producto por unidad se recibe en números enteros; el error dice el
// renglón.
func TestReceiveWholeUnits(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Receive(f.ctx, f.userID, &model.Purchase{SupplierID: f.supplierID, Total: dec("100"),
		Items: []*model.PurchaseItem{line(f.batched, "1.5", "50"), line(f.plain, "2.5", "10")}})
	if !errors.Is(err, model.ErrQuantityNotWhole) || !errors.Is(err, service.ErrValidation) {
		t.Fatalf("unit 2.5: got %v", err)
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("the error must name line 2: %v", err)
	}
}
