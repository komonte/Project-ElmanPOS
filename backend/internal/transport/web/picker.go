package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/createproduct"
)

// El buscador de producto de un renglón, compartido por compras (F-06) y
// conteos (F-09): busca por nombre, SKU o código (sirve escanear) y, si no
// encuentra, ofrece dar de alta lo escrito con el mismo formulario que
// Productos (product_form.gohtml con ForLine) o agregar el código escaneado
// a un producto que ya existe. Lo que se elige o se crea llega al renglón con
// el evento productChosen (HX-Trigger). El JavaScript está en
// picker_script.gohtml; cada página dice qué hacer con el producto elegido
// (pickerSetProduct).
//
// Los dos solo aceptan productos con stock propio: activos y que no sean
// combos (un combo se recibe o se cuenta por sus partes).

var (
	errLineProductInactive = fmt.Errorf("product is inactive: %w", service.ErrConflict)
	errLineComposite       = fmt.Errorf("a composite product has no stock of its own: %w", service.ErrValidation)
)

// PickerProductOptions handles GET /picker/product-options: los resultados
// del buscador de un renglón.
//
// Sin resultados ofrece dar de alta lo escrito y, si es un código de barras
// (IsCode), también agregárselo a un producto que ya existe.
func (h *WebHandler) PickerProductOptions(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.FormValue("product_q"))
	products, err := h.stockProducts(r.Context(), query)
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	_, codeErr := model.NormalizeBarcode(query)
	h.renderPartial(w, r, "picker_product_options.gohtml", map[string]any{
		"Products": products, "Query": query, "IsCode": codeErr == nil})
}

// stockProducts busca productos con stock propio: activos (lo hace
// SearchByName) y que no sean combos.
func (h *WebHandler) stockProducts(ctx context.Context, query string) ([]*model.Product, error) {
	found, err := h.productSvc.SearchByName(ctx, query)
	if err != nil {
		return nil, err
	}
	products := make([]*model.Product, 0, len(found))
	for _, p := range found {
		if p.SaleType != model.SaleTypeComposite {
			products = append(products, p)
		}
	}
	return products, nil
}

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

// PickerQuickProductForm handles GET /picker/quick-product?q=...&supplier_id=...:
// el formulario de producto con lo escrito en el renglón (código o nombre) y,
// desde una compra, su proveedor como proveedor preferido (casi siempre es
// ese; se puede cambiar). El cursor va al primer dato que falta.
func (h *WebHandler) PickerQuickProductForm(w http.ResponseWriter, r *http.Request) {
	name, barcode := quickProductPrefill(r.FormValue("q"))
	p := &model.Product{Name: name}
	if id, err := strconv.ParseInt(r.FormValue("supplier_id"), 10, 64); err == nil && id > 0 {
		p.PreferredSupplierID = &id
	}
	data := h.productFormData(r, p, "")
	data["ForLine"] = true
	data["Barcode"] = barcode
	data["Focus"] = "name"
	if name != "" {
		data["Focus"] = "category"
	}
	h.renderPartial(w, r, "product_form.gohtml", data)
}

// PickerQuickProductCreate handles POST /picker/quick-product. Lee el
// formulario igual que ProductsCreate y crea con createproduct (su propia
// transacción: no es parte de la compra o el conteo, que siguen abiertos). Si
// sale bien responde vacío (cierra el panel) y el evento productChosen para
// elegirlo en el renglón; si no, el formulario vuelve con lo escrito.
func (h *WebHandler) PickerQuickProductCreate(w http.ResponseWriter, r *http.Request) {
	product := &model.Product{}
	fail := func(errMsg string) {
		data := h.productFormData(r, product, errMsg)
		data["ForLine"] = true
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
// Los mismos datos que un resultado del buscador (pickerProductFrom);
// shelf_life es null sin días de vida.
func productChosenTrigger(p *model.Product) (string, error) {
	b, err := json.Marshal(map[string]any{"productChosen": map[string]any{
		"id": p.ID, "name": p.Name, "batches": p.ControlsBatches, "shelf_life": p.ShelfLifeDays}})
	return string(b), err
}

// PickerAssignCodeForm handles GET /picker/assign-code?code=...: el
// panel para agregar un código escaneado (que no está en el catálogo) a un
// producto que ya existe. Pasa con productos importados sin código o con una
// presentación nueva: darlo de alta otra vez lo duplicaría.
func (h *WebHandler) PickerAssignCodeForm(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, r, "picker_assign_code.gohtml", map[string]any{
		"Code": strings.TrimSpace(r.FormValue("code"))})
}

// PickerAssignCodeOptions handles GET /picker/assign-code/options: el
// buscador del panel (por nombre o SKU). Los mismos productos que el
// buscador del renglón.
func (h *WebHandler) PickerAssignCodeOptions(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.FormValue("q"))
	products, err := h.stockProducts(r.Context(), query)
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	h.renderPartial(w, r, "picker_assign_code_options.gohtml", map[string]any{
		"Products": products, "Query": query, "Code": strings.TrimSpace(r.FormValue("code"))})
}

// PickerAssignCode handles POST /picker/assign-code: agrega el código al
// producto con productcodes (F-02) y lo elige en el renglón (mismo evento que
// el alta rápida). Con error, el panel vuelve con el mensaje.
func (h *WebHandler) PickerAssignCode(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.FormValue("code"))
	productID, _ := strconv.ParseInt(r.FormValue("product_id"), 10, 64)
	fail := func(err error) {
		h.renderPartial(w, r, "picker_assign_code.gohtml", map[string]any{
			"Code": code, "Error": h.errMsg(r, err)})
	}

	// El producto tiene que tener stock propio: si no, el código quedaría
	// agregado pero el renglón no lo aceptaría.
	p, err := h.productSvc.GetByID(r.Context(), productID)
	switch {
	case err != nil:
		fail(err)
		return
	case !p.IsActive:
		fail(errLineProductInactive)
		return
	case p.SaleType == model.SaleTypeComposite:
		fail(errLineComposite)
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
