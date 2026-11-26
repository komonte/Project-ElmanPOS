package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/prices"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/purchases"
)

// Precios por factura (F-06, 2026-11-26). La lista de compras recibidas y
// el detalle de una factura los ven el admin y el repositor (permiso
// stock); poner precios, solo quien tiene el permiso prices. Lo mismo para
// la lista de productos con stock y sin precio (los que no vienen de una
// factura), que es solo del admin.

// PurchasesHistory handles GET /purchases/history (?unpriced=1: solo las
// facturas sin cotizar).
func (h *WebHandler) PurchasesHistory(w http.ResponseWriter, r *http.Request) {
	onlyUnpriced := r.URL.Query().Get("unpriced") == "1"
	list, err := h.purchases.List(r.Context(), onlyUnpriced)
	if err != nil {
		http.Error(w, h.errMsg(r, err), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "purchase_history.gohtml", map[string]any{
		"Section": "purchase_history", "TitleKey": "purchase.history_title",
		"Purchases": list, "OnlyUnpriced": onlyUnpriced, "Limit": purchases.ListLimit,
	})
}

// pricedView es una fila de la tabla de precios: el producto, lo que va en
// su campo (lo escrito, o lo propuesto) y si es la que falló.
type pricedView struct {
	purchases.PricedRow
	Value string
	Error bool
}

// pricedTable son los datos de priced_table.gohtml.
type pricedTable struct {
	Rows    []pricedView
	Action  string // a dónde se guardan los precios
	CanSet  bool   // puede poner precios (si no, solo mira)
	Invoice bool   // es una factura (estados y "Recibido"); si no, la lista de sin precio
	Pending int    // filas de productos activos sin cotizar
	Saved   int    // cuántos precios se acaban de guardar
	Error   string
}

// proposedPrice es lo que el campo de precio propone: sin precio, el
// sugerido (si hay); con precio, el actual (así "Guardar" lo confirma sin
// cambiarlo, y se cambia a mano mirando el sugerido).
func proposedPrice(lang string, row purchases.PricedRow) string {
	var v *decimal.Decimal
	switch {
	case row.Price != nil:
		v = row.Price
	case row.Suggested != nil:
		v = row.Suggested
	default:
		return ""
	}
	s := v.StringFixed(2)
	if lang == "es" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

// newPricedTable arma la tabla. typed son los valores escritos por producto
// (después de un error), y failed el producto que falló.
func newPricedTable(r *http.Request, rows []purchases.PricedRow, action string, invoice bool, typed map[int64]string, failed int64) pricedTable {
	lang := transport.GetLocale(r.Context())
	t := pricedTable{Action: action, CanSet: can(r, model.PermPrices), Invoice: invoice}
	for _, row := range rows {
		v, ok := typed[row.ProductID]
		if !ok {
			v = proposedPrice(lang, row)
		}
		t.Rows = append(t.Rows, pricedView{PricedRow: row, Value: v, Error: row.ProductID == failed})
		if row.Active && row.State != model.PriceOK {
			t.Pending++
		}
	}
	return t
}

// PurchasesInvoice handles GET /purchases/{id}: la factura con el estado del
// precio de cada producto y, para el admin, el formulario para cotizarla.
func (h *WebHandler) PurchasesInvoice(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	inv, err := h.purchases.Invoice(r.Context(), id)
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	h.render(w, r, "purchase_invoice.gohtml", map[string]any{
		"Section": "purchase_history", "TitleKey": "purchase.invoice_title", "Invoice": inv,
		"Table": newPricedTable(r, inv.Products, fmt.Sprintf("/purchases/%d/prices", inv.Summary.ID), true, nil, 0),
	})
}

// PurchasesSetPrices handles POST /purchases/{id}/prices: guarda los precios
// de la factura y responde la tabla de nuevo.
func (h *WebHandler) PurchasesSetPrices(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	inv, err := h.purchases.Invoice(r.Context(), id)
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	action := fmt.Sprintf("/purchases/%d/prices", inv.Summary.ID)
	h.savePrices(w, r, inv.Products, action, true, func() ([]purchases.PricedRow, error) {
		inv, err := h.purchases.Invoice(r.Context(), id)
		if err != nil {
			return nil, err
		}
		return inv.Products, nil
	})
}

// PricesMissing handles GET /prices/missing: los productos con stock y sin
// precio (no se pueden vender), para ponérselo.
func (h *WebHandler) PricesMissing(w http.ResponseWriter, r *http.Request) {
	rows, err := h.purchases.MissingPrices(r.Context())
	if err != nil {
		http.Error(w, h.errMsg(r, err), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "prices_missing.gohtml", map[string]any{
		"Section": "prices_missing", "TitleKey": "price.missing_title",
		"Table": newPricedTable(r, rows, "/prices/missing", false, nil, 0),
	})
}

// PricesMissingSet handles POST /prices/missing.
func (h *WebHandler) PricesMissingSet(w http.ResponseWriter, r *http.Request) {
	rows, err := h.purchases.MissingPrices(r.Context())
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	h.savePrices(w, r, rows, "/prices/missing", false, func() ([]purchases.PricedRow, error) {
		return h.purchases.MissingPrices(r.Context())
	})
}

// savePrices lee los precios del formulario y los guarda todos juntos
// (prices.SetMany). El formulario manda product_id y price por fila, en
// orden. Con only=<producto> se guarda esa fila; si no ("Guardar todos"),
// las que no están cotizadas y tienen un precio escrito. Las de productos
// desactivados no tienen campo. Responde la tabla: recargada si se guardó,
// o con lo escrito y la fila que falló.
func (h *WebHandler) savePrices(w http.ResponseWriter, r *http.Request, rows []purchases.PricedRow, action string,
	invoice bool, reload func() ([]purchases.PricedRow, error)) {
	if err := r.ParseForm(); err != nil {
		h.actionError(w, r, err)
		return
	}
	byID := make(map[int64]purchases.PricedRow, len(rows))
	for _, row := range rows {
		byID[row.ProductID] = row
	}
	ids, values := r.PostForm["product_id"], r.PostForm["price"]
	typed := map[int64]string{}
	var order []int64 // las filas en el orden del formulario
	for i := range ids {
		id, _ := strconv.ParseInt(ids[i], 10, 64)
		if i < len(values) {
			typed[id] = strings.TrimSpace(values[i])
			order = append(order, id)
		}
	}
	only, _ := strconv.ParseInt(r.FormValue("only"), 10, 64)

	var changes []prices.Change
	var failed int64
	var err error
	for _, id := range order {
		raw := typed[id]
		row, ok := byID[id]
		if !ok || !row.Active || (only != 0 && id != only) {
			continue
		}
		if only == 0 && (row.State == model.PriceOK || raw == "") {
			continue
		}
		price, ok := parseFormMoney(raw)
		if !ok {
			failed, err = id, model.ErrInvalidPrice
			break
		}
		changes = append(changes, prices.Change{ProductID: id, UnitPrice: price})
	}

	errMsg := ""
	switch {
	case err != nil:
	case len(changes) == 0:
		errMsg = h.tr(r, "price.nothing_to_save")
	default:
		_, err = h.prices.SetMany(r.Context(), transport.UserID(r.Context()), changes)
		var ce *prices.ChangeError
		if errors.As(err, &ce) {
			failed = ce.ProductID
		}
	}
	if err != nil {
		errMsg = fmt.Sprintf("%s: %s", byID[failed].Name, h.errMsg(r, err))
	}
	if errMsg != "" {
		// 200 con el error adentro: htmx no reemplaza la tabla con un 4xx.
		t := newPricedTable(r, rows, action, invoice, typed, failed)
		t.Error = errMsg
		h.renderPartial(w, r, "priced_table.gohtml", map[string]any{"Table": t})
		return
	}

	fresh, err := reload()
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	t := newPricedTable(r, fresh, action, invoice, nil, 0)
	t.Saved = len(changes)
	h.renderPartial(w, r, "priced_table.gohtml", map[string]any{"Table": t})
}
