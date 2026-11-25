package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/createproduct"
)

// quickProduct es lo escrito en el alta rápida de producto desde un renglón
// de compra, como texto, para volver a dibujar el formulario si algo falla.
//
// Es un alta corta: lo que hace falta para recibir y ponerle precio. Marca,
// proveedor, punto de pedido, etc. se completan después en la ficha.
type quickProduct struct {
	Name, Barcode, Margin string
	CategoryID            int64
	ByWeight              bool // se vende por kg (WEIGHT + KILOGRAM); si no, por unidad
	ControlsBatches       bool
}

// quickProductPrefill reparte lo que se escribió en el buscador del renglón:
// si es un código de barras válido (se escaneó, o se tipeó un código) va al
// código, y si no, al nombre.
func quickProductPrefill(q string) quickProduct {
	q = strings.TrimSpace(q)
	if _, err := model.NormalizeBarcode(q); err == nil {
		return quickProduct{Barcode: q}
	}
	return quickProduct{Name: q}
}

// quickProductFromForm lee el formulario. Devuelve el producto a crear y la
// clave i18n de un número mal escrito ("" si está bien).
func quickProductFromForm(r *http.Request) (quickProduct, *model.Product, string) {
	qp := quickProduct{
		Name:            r.FormValue("name"),
		Barcode:         strings.TrimSpace(r.FormValue("barcode")),
		Margin:          r.FormValue("margin_percent"),
		ByWeight:        r.FormValue("sold_by") == "kg",
		ControlsBatches: r.FormValue("controls_batches") != "",
	}
	qp.CategoryID, _ = strconv.ParseInt(r.FormValue("category_id"), 10, 64)

	p := &model.Product{Name: qp.Name, CategoryID: qp.CategoryID, ControlsBatches: qp.ControlsBatches,
		SaleType: model.SaleTypeUnit, MeasureUnit: model.MeasureUnitUnit}
	if qp.ByWeight {
		p.SaleType, p.MeasureUnit = model.SaleTypeWeight, model.MeasureUnitKilogram
	}
	margin, ok := parseFormDecimal(qp.Margin)
	if !ok {
		return qp, p, "error.product_margin"
	}
	p.MarginPercent = margin
	return qp, p, ""
}

// PurchasesQuickProductForm handles GET /purchases/quick-product?q=...: el
// formulario corto, con lo escrito en el renglón ya puesto (código o nombre).
func (h *WebHandler) PurchasesQuickProductForm(w http.ResponseWriter, r *http.Request) {
	h.renderQuickProduct(w, r, quickProductPrefill(r.FormValue("q")), "")
}

// PurchasesQuickProductCreate handles POST /purchases/quick-product. Crea el
// producto con createproduct (su propia transacción: no es parte de la
// compra, que todavía no se confirmó). Si sale bien responde vacío (cierra el
// modal) y el evento purchaseProductCreated con {id, name, batches}: la
// página lo elige en el renglón que pidió el alta. Si no, el formulario
// vuelve con lo escrito y el error.
func (h *WebHandler) PurchasesQuickProductCreate(w http.ResponseWriter, r *http.Request) {
	qp, p, errKey := quickProductFromForm(r)
	if errKey != "" {
		h.renderQuickProduct(w, r, qp, h.tr(r, errKey))
		return
	}
	created, err := h.createProduct.Create(r.Context(), createproduct.Request{Product: p, Barcode: qp.Barcode})
	if err != nil {
		h.renderQuickProduct(w, r, qp, h.errMsg(r, err))
		return
	}
	trigger, err := quickProductTrigger(created)
	if err != nil {
		h.renderQuickProduct(w, r, qp, h.errMsg(r, err))
		return
	}
	w.Header().Set("HX-Trigger", trigger)
	w.WriteHeader(http.StatusOK)
}

// quickProductTrigger arma el header HX-Trigger del producto creado. Los
// mismos datos que un resultado del buscador (purchasePickProduct).
func quickProductTrigger(p *model.Product) (string, error) {
	b, err := json.Marshal(map[string]any{"purchaseProductCreated": map[string]any{
		"id": p.ID, "name": p.Name, "batches": p.ControlsBatches}})
	return string(b), err
}

func (h *WebHandler) renderQuickProduct(w http.ResponseWriter, r *http.Request, qp quickProduct, errMsg string) {
	categories, err := h.categorySvc.GetAllIncludingInactive(r.Context())
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	// Solo activas: un producto nuevo no puede ir a una categoría inactiva.
	opts := productCategoryOptions(categories, qp.CategoryID)
	active := opts[:0]
	for _, o := range opts {
		if !o.Inactive {
			active = append(active, o)
		}
	}
	h.renderPartial(w, r, "purchase_quick_product.gohtml", map[string]any{
		"Product": qp, "Categories": active, "Error": errMsg})
}
