package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/createproduct"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/purchases"
)

// El alta de producto desde un renglón de compra usa el mismo formulario que
// Productos (product_form.gohtml con ForPurchase): categoría o marca nuevas,
// proveedor, etc., sin salir de la compra. Lo que cambia es adónde guarda y
// qué pasa después (ver PurchasesQuickProductCreate).

// quickProductPrefill reparte lo que se escribió en el buscador del renglón:
// si es un código de barras válido (se escaneó, o se tipeó un código) va al
// código, y si no, al nombre.
func quickProductPrefill(q string) (name, barcode string) {
	q = strings.TrimSpace(q)
	if _, err := model.NormalizeBarcode(q); err == nil {
		return "", q
	}
	return q, ""
}

// PurchasesQuickProductForm handles GET /purchases/quick-product?q=...&supplier_id=...:
// el formulario de producto con lo escrito en el renglón (código o nombre) y,
// como proveedor preferido, el de la compra (casi siempre es ese; se puede
// cambiar). El cursor va al primer dato que falta.
func (h *WebHandler) PurchasesQuickProductForm(w http.ResponseWriter, r *http.Request) {
	name, barcode := quickProductPrefill(r.FormValue("q"))
	p := &model.Product{Name: name}
	if id, err := strconv.ParseInt(r.FormValue("supplier_id"), 10, 64); err == nil && id > 0 {
		p.PreferredSupplierID = &id
	}
	data := h.productFormData(r, p, "")
	data["ForPurchase"] = true
	data["Barcode"] = barcode
	data["Focus"] = "name"
	if name != "" {
		data["Focus"] = "category"
	}
	h.renderPartial(w, r, "product_form.gohtml", data)
}

// PurchasesQuickProductCreate handles POST /purchases/quick-product. Lee el
// formulario igual que ProductsCreate y crea con createproduct (su propia
// transacción: no es parte de la compra, que todavía no se confirmó). Si sale
// bien responde vacío (cierra el panel) y el evento purchaseProductChosen
// para elegirlo en el renglón; si no, el formulario vuelve con lo escrito.
func (h *WebHandler) PurchasesQuickProductCreate(w http.ResponseWriter, r *http.Request) {
	product := &model.Product{}
	fail := func(errMsg string) {
		data := h.productFormData(r, product, errMsg)
		data["ForPurchase"] = true
		h.renderPartial(w, r, "product_form.gohtml", data)
	}
	if err := r.ParseForm(); err != nil {
		fail(h.errMsg(r, err))
		return
	}
	if errKey := productFromForm(r, product); errKey != "" {
		fail(h.tr(r, errKey))
		return
	}
	created, err := h.createProduct.Create(r.Context(), createproduct.Request{
		Product:     product,
		Barcode:     r.FormValue("barcode"),
		NewBrand:    r.FormValue("brand_new"),
		NewCategory: newCategoryFromForm(r),
	})
	if err != nil {
		fail(h.errMsg(r, err))
		return
	}
	trigger, err := productChosenTrigger(created)
	if err != nil {
		fail(h.errMsg(r, err))
		return
	}
	w.Header().Set("HX-Trigger", trigger)
	w.WriteHeader(http.StatusOK)
}

// productChosenTrigger arma el header HX-Trigger que elige un producto en el
// renglón que lo pidió (producto creado, o al que se le agregó el código).
// Los mismos datos que un resultado del buscador (purchasePickProduct).
func productChosenTrigger(p *model.Product) (string, error) {
	b, err := json.Marshal(map[string]any{"purchaseProductChosen": map[string]any{
		"id": p.ID, "name": p.Name, "batches": p.ControlsBatches}})
	return string(b), err
}

// PurchasesAssignCodeForm handles GET /purchases/assign-code?code=...: el
// panel para agregar un código escaneado (que no está en el catálogo) a un
// producto que ya existe. Pasa con productos importados sin código o con una
// presentación nueva: darlo de alta otra vez lo duplicaría.
func (h *WebHandler) PurchasesAssignCodeForm(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, r, "purchase_assign_code.gohtml", map[string]any{
		"Code": strings.TrimSpace(r.FormValue("code"))})
}

// PurchasesAssignCodeOptions handles GET /purchases/assign-code/options: el
// buscador del panel (por nombre o SKU). Los mismos productos que se pueden
// recibir.
func (h *WebHandler) PurchasesAssignCodeOptions(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.FormValue("q"))
	products, err := h.receivableProducts(r.Context(), query)
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	h.renderPartial(w, r, "purchase_assign_code_options.gohtml", map[string]any{
		"Products": products, "Query": query, "Code": strings.TrimSpace(r.FormValue("code"))})
}

// PurchasesAssignCode handles POST /purchases/assign-code: agrega el código al
// producto con productcodes (F-02) y lo elige en el renglón (mismo evento que
// el alta rápida). Con error, el panel vuelve con el mensaje.
func (h *WebHandler) PurchasesAssignCode(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.FormValue("code"))
	productID, _ := strconv.ParseInt(r.FormValue("product_id"), 10, 64)
	fail := func(err error) {
		h.renderPartial(w, r, "purchase_assign_code.gohtml", map[string]any{
			"Code": code, "Error": h.errMsg(r, err)})
	}

	// El producto tiene que poder recibirse: si no, el código quedaría
	// agregado pero el renglón no lo aceptaría.
	p, err := h.productSvc.GetByID(r.Context(), productID)
	switch {
	case err != nil:
		fail(err)
		return
	case !p.IsActive:
		fail(purchases.ErrProductInactive)
		return
	case p.SaleType == model.SaleTypeComposite:
		fail(purchases.ErrCompositeProduct)
		return
	}
	if _, err := h.productCodes.Add(r.Context(), p.ID, code); err != nil {
		fail(err)
		return
	}
	trigger, err := productChosenTrigger(p)
	if err != nil {
		fail(err)
		return
	}
	w.Header().Set("HX-Trigger", trigger)
	w.WriteHeader(http.StatusOK)
}
