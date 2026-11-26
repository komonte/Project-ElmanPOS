package stockcounts

import (
	"errors"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/countcsv"
	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store/postgres"
)

// importFixture: el producto común tiene un código de barras corto (como el
// de una balanza) y el que controla lotes se busca por su SKU.
func importFixture(t *testing.T) (f *fixture, batchedSKU string) {
	t.Helper()
	f = newFixture(t)
	f.exec(t, `INSERT INTO product_codes (product_id, code, code_type) VALUES ($1, '0000009770301', 'INTERNAL');`, f.plain)
	f.scan(t, &batchedSKU, `SELECT sku FROM products WHERE id = $1;`, f.batched)
	return f, batchedSKU
}

func row(line int, code, qty string) countcsv.Row {
	return countcsv.Row{Line: line, Code: code, Quantity: dec(qty)}
}

func TestImportPreview(t *testing.T) {
	f, sku := importFixture(t)
	f.newProduct(t, "INACTIVO TEST COUNTS", `'UNIT', 'UNIT', FALSE, 30, FALSE`)
	var inactiveSKU, compositeSKU string
	f.scan(t, &inactiveSKU, `SELECT sku FROM products WHERE name = 'INACTIVO TEST COUNTS';`)
	f.scan(t, &compositeSKU, `SELECT sku FROM products WHERE id = $1;`, f.composite)
	c := f.open(t)

	withExpiry := row(5, "9770301", "1")
	withExpiry.Expiry = "2027-03-31"
	zero := dec("0")
	badCost := row(9, "9770301", "1")
	badCost.Cost = &zero
	rows := []countcsv.Row{
		row(2, "9770301", "12"), // código corto: se normaliza como se guarda
		{Line: 3, Code: sku, Quantity: dec("2.5"), Sector: "Heladera"},
		row(4, "7799999999999", "1"), // no existe
		withExpiry,                   // vencimiento sin lotes
		row(6, inactiveSKU, "1"),
		row(7, compositeSKU, "1"),
		row(8, "9770301", "-1"),
		badCost,
	}
	readerProblems := []countcsv.LineError{{Line: 10, Err: countcsv.ErrQuantityInvalid}, {Line: 1, Err: countcsv.ErrCodeMissing}}

	res, err := f.svc.Import(f.ctx, c.ID, rows, readerProblems, "  Depósito  ", false)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if res.Applied || len(res.Lines) != 2 {
		t.Fatalf("lines: got %+v (applied %v)", res.Lines, res.Applied)
	}
	if l := res.Lines[0]; l.ProductID != f.plain || l.Sector != "Depósito" || l.ProductName != "GASEOSA TEST COUNTS" {
		t.Errorf("line 2: got %+v", l)
	}
	if l := res.Lines[1]; l.ProductID != f.batched || l.Sector != "Heladera" {
		t.Errorf("line 3: got %+v", l)
	}
	want := []struct {
		line int
		err  error
	}{
		{1, countcsv.ErrCodeMissing}, {4, ErrCodeNotFound}, {5, ErrExpiryNotAllowed}, {6, ErrProductInactive},
		{7, ErrCompositeProduct}, {8, model.ErrInvalidCountedQuantity}, {9, model.ErrInvalidCost},
		{10, countcsv.ErrQuantityInvalid},
	}
	if len(res.Problems) != len(want) {
		t.Fatalf("problems: got %+v", res.Problems)
	}
	for i, w := range want {
		if p := res.Problems[i]; p.Line != w.line || !errors.Is(p.Err, w.err) {
			t.Errorf("problem %d: got line %d %v, want line %d %v", i, p.Line, p.Err, w.line, w.err)
		}
	}
	if res.Problems[1].Code != "7799999999999" {
		t.Errorf("problem keeps the code: got %q", res.Problems[1].Code)
	}
	// La vista previa no guarda nada.
	if got, _ := f.svc.Get(f.ctx, c.ID); len(got.Items) != 0 {
		t.Errorf("preview saved items: %+v", got.Items)
	}
}

func TestImportApply(t *testing.T) {
	f, sku := importFixture(t)
	f.addStock(t, f.plain, "10")
	c := f.open(t)
	f.add(t, c.ID, f.plain, "Góndola", "3") // ya había una carga a mano

	cost1, cost2 := dec("100"), dec("120")
	r1 := row(2, "9770301", "5")
	r1.Cost = &cost1
	r2 := countcsv.Row{Line: 3, Code: "0000009770301", Quantity: dec("2"), Sector: "Heladera", Cost: &cost2}
	r3 := countcsv.Row{Line: 4, Code: sku, Quantity: dec("1.5"), Expiry: "2027-03-31"}
	r4 := countcsv.Row{Line: 5, Code: sku, Quantity: dec("0.5")}
	cost3 := dec("4000")
	r4.Cost = &cost3

	res, err := f.svc.Import(f.ctx, c.ID, []countcsv.Row{r1, r2, r3, r4, row(6, "nada", "1")}, nil, "Depósito", true)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if !res.Applied || len(res.Lines) != 4 || len(res.Problems) != 1 {
		t.Fatalf("result: applied %v, %d lines, problems %+v", res.Applied, len(res.Lines), res.Problems)
	}

	got, _ := f.svc.Get(f.ctx, c.ID)
	if len(got.Items) != 2 {
		t.Fatalf("items: got %+v", got.Items)
	}
	g, q := got.Items[0], got.Items[1] // GASEOSA, QUESO
	// La foto es la de la primera carga (a mano); todo se suma.
	if !g.SystemQuantity.Equal(dec("10")) || !g.CountedQuantity.Equal(dec("10")) || len(g.Entries) != 3 ||
		g.Entries[1].Sector != "Depósito" || g.Entries[2].Sector != "Heladera" {
		t.Errorf("gaseosa: got %+v, entries %+v", g, g.Entries)
	}
	if g.UnitCost == nil || !g.UnitCost.Equal(cost2) {
		t.Errorf("gaseosa cost: got %v, want the last one (120)", g.UnitCost)
	}
	// Una fila con vencimiento y otra con costo: queda cada dato.
	if !q.CountedQuantity.Equal(dec("2")) || q.ExpiryDate != "2027-03-31" || q.UnitCost == nil || !q.UnitCost.Equal(cost3) {
		t.Errorf("queso: got counted %s expiry %q cost %v", q.CountedQuantity, q.ExpiryDate, q.UnitCost)
	}
}

func TestImportErrors(t *testing.T) {
	f, _ := importFixture(t)
	c := f.open(t)
	if _, err := f.svc.Import(f.ctx, c.ID, nil, nil, "  ", false); !errors.Is(err, ErrImportSectorRequired) {
		t.Errorf("no sector: got %v", err)
	}
	if _, err := f.svc.Import(f.ctx, 999999999, nil, nil, "A", false); !errors.Is(err, service.ErrNotFound) {
		t.Errorf("missing count: got %v", err)
	}
	if _, err := postgres.NewStockCountStore(f.tx).Confirm(f.ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Import(f.ctx, c.ID, []countcsv.Row{row(2, "9770301", "1")}, nil, "A", true); !errors.Is(err, service.ErrConflict) {
		t.Errorf("confirmed count: got %v", err)
	}
}
