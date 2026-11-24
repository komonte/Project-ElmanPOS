package web

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/createproduct"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/productcodes"
	"github.com/shopspring/decimal"
)

// Pantalla de productos. Igual que categorías (lista, búsqueda, panel con
// formulario, desactivar / reactivar, borrar). El formulario elige categoría,
// marca y proveedor entre los activos, más el actual aunque esté inactivo.

// productOption es una opción de un <select> del formulario.
type productOption struct {
	ID       int64
	Label    string
	Selected bool
	Inactive bool
}

// activeOptions arma las opciones de un <select> de una tabla maestra: las
// activas más la actual aunque esté inactiva (si no, se perdería al guardar;
// el store deja conservarla). fields saca id, nombre y si está activa.
func activeOptions[T any](list []*T, current *int64, fields func(*T) (int64, string, bool)) []productOption {
	opts := make([]productOption, 0, len(list))
	for _, x := range list {
		id, name, active := fields(x)
		isCurrent := current != nil && *current == id
		if !active && !isCurrent {
			continue
		}
		opts = append(opts, productOption{ID: id, Label: name, Selected: isCurrent, Inactive: !active})
	}
	return opts
}

func supplierOptions(suppliers []*model.Supplier, current *int64) []productOption {
	return activeOptions(suppliers, current, func(s *model.Supplier) (int64, string, bool) {
		return s.ID, s.Name, s.IsActive
	})
}

// productCategoryOptions muestra cada categoría con su camino completo
// ("Almacen › Varios"), ordenadas por camino: un mismo nombre puede estar en
// dos lugares del árbol.
func productCategoryOptions(all []*model.Category, current int64) []productOption {
	paths := categoryPaths(all)
	opts := activeOptions(all, &current, func(c *model.Category) (int64, string, bool) {
		return c.ID, paths[c.ID], c.IsActive
	})
	sort.SliceStable(opts, func(i, j int) bool {
		return strings.ToLower(opts[i].Label) < strings.ToLower(opts[j].Label)
	})
	return opts
}

// saleTypeOptions son los tipos de venta que ofrece el formulario. COMPOSITE
// (combo) solo aparece si el producto ya lo es: hasta que se puedan cargar
// sus componentes (F-05), un combo nuevo quedaría vacío.
func saleTypeOptions(current model.SaleType) []model.SaleType {
	opts := make([]model.SaleType, 0, len(model.SaleTypes))
	for _, st := range model.SaleTypes {
		if st == model.SaleTypeComposite && current != model.SaleTypeComposite {
			continue
		}
		opts = append(opts, st)
	}
	return opts
}

// parseFormDecimal lee un número del formulario. Acepta coma decimal
// ("2,5"), que es como se escribe acá. Vacío es cero.
func parseFormDecimal(s string) (decimal.Decimal, bool) {
	s = strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	if s == "" {
		return decimal.Zero, true
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return decimal.Zero, false
	}
	return d, true
}

// optionalID lee un <select> opcional: vacío es "sin dato" (nil). Un número
// mal escrito queda en 0, y Validate() lo rechaza.
func optionalID(s string) *int64 {
	if s == "" {
		return nil
	}
	id, _ := strconv.ParseInt(s, 10, 64)
	return &id
}

// productFromForm copia los campos del formulario a p. Si un número está mal
// escrito devuelve la clave i18n del error ("" si están todos bien); los
// demás campos se copian igual, así el formulario vuelve con lo escrito.
func productFromForm(r *http.Request, p *model.Product) string {
	p.SKU = r.FormValue("sku")
	p.Name = r.FormValue("name")
	p.Description = r.FormValue("description")
	p.CategoryID, _ = strconv.ParseInt(r.FormValue("category_id"), 10, 64)
	p.BrandID = optionalID(r.FormValue("brand_id"))
	// La marca se elige con un buscador: brand_q es el texto escrito y
	// brand_id la que se eligió de la lista. Si hay texto pero no se eligió
	// ninguna, se avisa en vez de guardar el producto sin marca sin querer.
	brandTyped := strings.TrimSpace(r.FormValue("brand_q")) != ""
	p.PreferredSupplierID = optionalID(r.FormValue("preferred_supplier_id"))
	p.SaleType = model.SaleType(r.FormValue("sale_type"))
	p.MeasureUnit = model.MeasureUnit(r.FormValue("measure_unit"))
	// Un checkbox sin tildar no viene en el formulario.
	p.ControlsBatches = r.FormValue("controls_batches") != ""
	p.IsConsignment = r.FormValue("is_consignment") != ""

	errKey := ""
	if brandTyped && p.BrandID == nil {
		errKey = "error.product_brand_pick"
	}
	if d, ok := parseFormDecimal(r.FormValue("reorder_point")); ok {
		p.ReorderPoint = d
	} else if errKey == "" {
		errKey = "error.product_reorder_point"
	}

	p.MaxStock = nil // vacío: sin tope
	if v := strings.TrimSpace(r.FormValue("max_stock")); v != "" {
		if d, ok := parseFormDecimal(v); ok {
			p.MaxStock = &d
		} else if errKey == "" {
			errKey = "error.product_max_stock"
		}
	}

	if d, ok := parseFormDecimal(r.FormValue("margin_percent")); ok {
		p.MarginPercent = d
	} else if errKey == "" {
		errKey = "error.product_margin"
	}
	return errKey
}

// renderProductForm dibuja el panel con el formulario. Arma las opciones de
// los <select> cada vez, también al volver con un error.
func (h *WebHandler) renderProductForm(w http.ResponseWriter, r *http.Request, p *model.Product, errMsg string) {
	ctx := r.Context()
	data := map[string]any{"Product": p, "Error": errMsg}
	// Alta: si vuelve con un error, conserva el código de barras escrito.
	// Edición: los códigos van en su propia sección (product_codes.gohtml).
	if p.ID == 0 {
		data["Barcode"] = r.FormValue("barcode")
	} else {
		codes, err := h.productCodes.List(ctx, p.ID)
		if err != nil {
			data["Error"] = h.errMsg(r, err)
			h.renderPartial(w, r, "product_form.gohtml", data)
			return
		}
		data["Codes"] = codes
	}

	categories, err := h.categorySvc.GetAllIncludingInactive(ctx)
	if err != nil {
		data["Error"] = h.errMsg(r, err)
		h.renderPartial(w, r, "product_form.gohtml", data)
		return
	}
	// La marca no es un <select>: con miles de marcas se busca (ver
	// ProductsBrandOptions). Solo hace falta el nombre de la actual.
	if p.BrandID != nil {
		if b, err := h.brandSvc.GetByID(ctx, *p.BrandID); err == nil {
			data["BrandName"] = b.Name
			data["BrandInactive"] = !b.IsActive
		}
	}
	suppliers, err := h.supplierSvc.GetAllIncludingInactive(ctx)
	if err != nil {
		data["Error"] = h.errMsg(r, err)
		h.renderPartial(w, r, "product_form.gohtml", data)
		return
	}

	data["Categories"] = productCategoryOptions(categories, p.CategoryID)
	data["Suppliers"] = supplierOptions(suppliers, p.PreferredSupplierID)
	data["SaleTypes"] = saleTypeOptions(p.SaleType)
	data["MeasureUnits"] = model.MeasureUnits
	h.renderPartial(w, r, "product_form.gohtml", data)
}

// productsTableData arma los datos de la tabla (ver adminList).
func (h *WebHandler) productsTableData(r *http.Request) (map[string]any, error) {
	list, total, err := adminList(r, h.productSvc)
	if err != nil {
		return nil, err
	}
	return map[string]any{"Products": list, "Shown": len(list), "Total": total}, nil
}

// ProductsBrandOptions handles GET /products/brand-options: las marcas
// activas que coinciden con lo escrito en el buscador del formulario (hasta
// 20, ver SearchByName). Elegir una inactiva no se ofrece.
func (h *WebHandler) ProductsBrandOptions(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.FormValue("brand_q"))
	brands, err := h.brandSvc.SearchByName(r.Context(), query)
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	h.renderPartial(w, r, "product_brand_options.gohtml", map[string]any{"Brands": brands, "Query": query})
}

// ProductsBarcodeCheck handles GET /products/barcode-check: revisa el código
// que se escaneó en el alta, antes de llenar el resto. Si ya es de un
// producto, lo dice y ofrece editarlo; si está libre, no muestra nada.
// Es solo un aviso: al guardar se vuelve a revisar.
func (h *WebHandler) ProductsBarcodeCheck(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{}
	if raw := strings.TrimSpace(r.FormValue("barcode")); raw != "" {
		err := h.productCodes.Check(r.Context(), raw)
		var taken *productcodes.CodeTakenError
		switch {
		case errors.As(err, &taken):
			data["OwnerID"] = taken.ProductID
			data["Message"] = fmt.Sprintf(h.tr(r, "product.barcode_exists"), taken.ProductSKU, taken.ProductName)
		case err != nil:
			data["Message"] = h.errMsg(r, err)
		}
	}
	h.renderPartial(w, r, "product_barcode_check.gohtml", data)
}

// ProductsList handles GET /products
func (h *WebHandler) ProductsList(w http.ResponseWriter, r *http.Request) {
	data, err := h.productsTableData(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") != "" {
		h.renderPartial(w, r, "product_table.gohtml", data)
		return
	}

	data["Section"] = "products"
	data["TitleKey"] = "product.title"
	h.render(w, r, "products.gohtml", data)
}

// ProductsNewForm handles GET /products/new
func (h *WebHandler) ProductsNewForm(w http.ResponseWriter, r *http.Request) {
	h.renderProductForm(w, r, &model.Product{}, "")
}

// ProductsCreate handles POST /products/new. El código de barras es
// opcional: si viene, se guarda junto con el producto en una sola
// transacción (caso de uso createproduct).
func (h *WebHandler) ProductsCreate(w http.ResponseWriter, r *http.Request) {
	product := &model.Product{}
	if err := r.ParseForm(); err != nil {
		h.renderProductForm(w, r, product, h.errMsg(r, err))
		return
	}

	if errKey := productFromForm(r, product); errKey != "" {
		h.renderProductForm(w, r, product, h.tr(r, errKey))
		return
	}
	if _, err := h.createProduct.Create(r.Context(), createproduct.Request{Product: product, Barcode: r.FormValue("barcode")}); err != nil {
		h.renderProductForm(w, r, product, h.errMsg(r, err))
		return
	}

	w.Header().Set("HX-Redirect", "/products")
	w.WriteHeader(http.StatusOK)
}

// ProductsEditForm handles GET /products/{id}/edit
func (h *WebHandler) ProductsEditForm(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	product, err := h.productSvc.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, h.errMsg(r, err), http.StatusNotFound)
		return
	}

	h.renderProductForm(w, r, product, "")
}

// ProductsUpdate handles POST /products/{id}/edit
func (h *WebHandler) ProductsUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		h.renderProductForm(w, r, &model.Product{}, h.tr(r, "error.invalid_id"))
		return
	}

	// Se parte del producto guardado: el formulario no trae is_active.
	product, err := h.productSvc.GetByID(ctx, id)
	if err != nil {
		h.renderProductForm(w, r, &model.Product{}, h.errMsg(r, err))
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderProductForm(w, r, product, h.errMsg(r, err))
		return
	}

	if errKey := productFromForm(r, product); errKey != "" {
		h.renderProductForm(w, r, product, h.tr(r, errKey))
		return
	}
	if _, err := h.productSvc.Update(ctx, id, product); err != nil {
		h.renderProductForm(w, r, product, h.errMsg(r, err))
		return
	}

	w.Header().Set("HX-Redirect", "/products")
	w.WriteHeader(http.StatusOK)
}

// ProductsDelete handles DELETE /products/{id}
func (h *WebHandler) ProductsDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.productSvc.Delete(r.Context(), id); err != nil {
		h.actionError(w, r, err)
		return
	}

	h.productsTable(w, r)
}

// ProductsDeactivate handles PATCH /products/{id}/deactivate
func (h *WebHandler) ProductsDeactivate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.productSvc.Deactivate(r.Context(), id); err != nil {
		h.actionError(w, r, err)
		return
	}

	h.productsTable(w, r)
}

// ProductsReactivate handles PATCH /products/{id}/reactivate
func (h *WebHandler) ProductsReactivate(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.productSvc.Reactivate(r.Context(), id); err != nil {
		h.actionError(w, r, err)
		return
	}

	h.productsTable(w, r)
}

// productsTable redibuja la tabla después de una acción.
func (h *WebHandler) productsTable(w http.ResponseWriter, r *http.Request) {
	data, err := h.productsTableData(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPartial(w, r, "product_table.gohtml", data)
}

// renderProductCodes redibuja la sección de códigos de un producto, con un
// error (y lo que se escribió) si la acción falló. focus vuelve a poner el
// cursor en el campo: después de agregar, se puede escanear el siguiente.
func (h *WebHandler) renderProductCodes(w http.ResponseWriter, r *http.Request, productID int64, errMsg, value string, focus bool) {
	data := map[string]any{"ProductID": productID, "CodeError": errMsg, "CodeValue": value, "Focus": focus}
	codes, err := h.productCodes.List(r.Context(), productID)
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	data["Codes"] = codes
	h.renderPartial(w, r, "product_codes.gohtml", data)
}

// ProductCodesAdd handles POST /products/{id}/codes. Se guarda al momento,
// sin esperar al "Guardar" del producto (F-02).
func (h *WebHandler) ProductCodesAdd(w http.ResponseWriter, r *http.Request) {
	productID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	code := r.FormValue("code")
	if _, err := h.productCodes.Add(r.Context(), productID, code); err != nil {
		// Un producto que no existe (lo borraron con el panel abierto) no
		// tiene sección que redibujar: va como error de acción.
		if errors.Is(err, service.ErrNotFound) || errors.Is(err, service.ErrInvalidID) {
			h.actionError(w, r, err)
			return
		}
		h.renderProductCodes(w, r, productID, h.errMsg(r, err), code, true)
		return
	}
	h.renderProductCodes(w, r, productID, "", "", true)
}

// ProductCodesRemove handles DELETE /products/{id}/codes/{codeID}.
func (h *WebHandler) ProductCodesRemove(w http.ResponseWriter, r *http.Request) {
	productID, err1 := strconv.ParseInt(r.PathValue("id"), 10, 64)
	codeID, err2 := strconv.ParseInt(r.PathValue("codeID"), 10, 64)
	if err1 != nil || err2 != nil {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	if err := h.productCodes.Remove(r.Context(), productID, codeID); err != nil {
		h.actionError(w, r, err)
		return
	}
	h.renderProductCodes(w, r, productID, "", "", false)
}
