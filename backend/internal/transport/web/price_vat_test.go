package web

import (
	"bytes"
	"strings"
	"testing"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/shopspring/decimal"
)

func TestPriceVatNote(t *testing.T) {
	cases := []struct {
		lang, rate, want string
	}{
		{"es", "21", "IVA 21 % incluido"},
		{"es", "10.5", "IVA 10,5 % incluido"},
		{"es", "0", "sin IVA"},
		{"en", "10.5", "VAT 10.5 % included"},
		{"en", "0", "no VAT"},
	}
	for _, c := range cases {
		if got := priceVatNote(c.lang, decimal.RequireFromString(c.rate)); got != c.want {
			t.Errorf("%s %s: got %q, want %q", c.lang, c.rate, got, c.want)
		}
	}
}

func TestCategoryVatNotes(t *testing.T) {
	all := []*model.Category{
		{ID: 1, Name: "Almacén", TaxID: 10, IsActive: true},
		{ID: 2, Name: "Varios 0%", TaxID: 30, IsActive: true},
	}
	rates := taxRates([]*model.Tax{
		{ID: 10, Rate: decimal.NewFromInt(21)},
		{ID: 30, Rate: decimal.Zero},
	})
	opts := withCategoryVatNotes("es", productCategoryOptions(all, 2), all, rates)
	got := map[int64]string{}
	for _, o := range opts {
		got[o.ID] = o.VatNote
	}
	if got[1] != "IVA 21 % incluido" || got[2] != "sin IVA" {
		t.Errorf("got %v", got)
	}

	// La etiqueta del alta arranca con la categoría elegida, o con el
	// impuesto de la categoría nueva si está abierta.
	data := map[string]any{"Categories": opts}
	if n := initialVatNote(data); n != "sin IVA" {
		t.Errorf("chosen category: got %q", n)
	}
	data["NewCategory"] = &model.Category{}
	data["Taxes"] = []productOption{{ID: 10, VatNote: "IVA 21 % incluido", Selected: true}}
	if n := initialVatNote(data); n != "IVA 21 % incluido" {
		t.Errorf("new category: got %q", n)
	}
	data["Taxes"] = []productOption{{ID: 10, VatNote: "IVA 21 % incluido"}}
	if n := initialVatNote(data); n != "" {
		t.Errorf("new category without tax: got %q", n)
	}
}

func TestRenderPriceVatLabel(t *testing.T) {
	e := NewTemplateEngine()
	render := func(name string, data map[string]any) string {
		data["Can"] = adminCan
		var buf bytes.Buffer
		if err := e.Render(&buf, "es", name, data); err != nil {
			t.Fatal(err)
		}
		return buf.String()
	}
	form := func(data map[string]any) string {
		data["SaleTypes"] = saleTypeOptions("")
		data["MeasureUnits"] = model.MeasureUnits
		return render("product_form.gohtml", data)
	}

	// Alta sin categoría: el texto general, y cada opción con su nota.
	cats := []productOption{{ID: 1, Label: "Almacén", VatNote: "IVA 21 % incluido"}}
	html := form(map[string]any{"Product": &model.Product{}, "Categories": cats})
	for _, s := range []string{`Precio de venta`, `data-default="lo que paga el cliente">lo que paga el cliente</span>`,
		`data-vat="IVA 21 % incluido"`, `onchange="productPriceVat(this.form)"`} {
		if !strings.Contains(html, s) {
			t.Errorf("new: missing %q", s)
		}
	}

	// Vuelve con un error y una categoría al 0 %: ya dice "sin IVA".
	html = form(map[string]any{"Product": &model.Product{}, "Categories": cats, "VatNote": "sin IVA"})
	if !strings.Contains(html, `data-default="lo que paga el cliente">sin IVA</span>`) {
		t.Error("new with category: label without the note")
	}

	// Editar: la sección de precio lleva la nota de su categoría.
	html = render("product_prices.gohtml", map[string]any{"ProductID": 7, "VatNote": "IVA 10,5 % incluido"})
	if !strings.Contains(html, `(IVA 10,5 % incluido)`) {
		t.Error("prices: missing the note")
	}
	html = render("product_prices.gohtml", map[string]any{"ProductID": 7})
	if strings.Contains(html, `<span class="font-normal text-muted">(`) {
		t.Error("prices: empty note shown")
	}
}
