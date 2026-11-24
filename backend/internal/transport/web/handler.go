package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/i18n"
	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/shopspring/decimal"
)

type WebHandler struct {
	engine      *TemplateEngine
	brandSvc    *service.Service[model.Brand]
	taxSvc      *service.Service[model.Tax]
	supplierSvc *service.Service[model.Supplier]
}

func NewWebHandler(engine *TemplateEngine, brandSvc *service.Service[model.Brand], taxSvc *service.Service[model.Tax], supplierSvc *service.Service[model.Supplier]) *WebHandler {
	return &WebHandler{
		engine:      engine,
		brandSvc:    brandSvc,
		taxSvc:      taxSvc,
		supplierSvc: supplierSvc,
	}
}

func (h *WebHandler) render(w http.ResponseWriter, r *http.Request, page string, data map[string]any) {
	lang := transport.GetLocale(r.Context())
	if data == nil {
		data = make(map[string]any)
	}
	data["Lang"] = lang
	data["T"] = func(key string) string { return i18n.T(lang, key) }
	if _, ok := data["Section"]; !ok {
		data["Section"] = ""
	}
	if _, ok := data["TitleKey"]; !ok {
		data["TitleKey"] = ""
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.engine.Render(w, lang, page, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *WebHandler) renderPartial(w http.ResponseWriter, r *http.Request, partial string, data map[string]any, statusCode ...int) {
	lang := transport.GetLocale(r.Context())
	if data == nil {
		data = make(map[string]any)
	}
	data["Lang"] = lang
	data["T"] = func(key string) string { return i18n.T(lang, key) }
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if len(statusCode) > 0 {
		w.WriteHeader(statusCode[0])
	}
	if err := h.engine.Render(w, lang, partial, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *WebHandler) errMsg(r *http.Request, err error) string {
	lang := transport.GetLocale(r.Context())
	switch {
	case errors.Is(err, service.ErrAlreadyExists):
		return i18n.T(lang, "error.already_exists")
	case errors.Is(err, service.ErrNotFound):
		return i18n.T(lang, "error.not_found")
	case errors.Is(err, service.ErrInUse):
		return i18n.T(lang, "error.in_use")
	default:
		return err.Error()
	}
}

// setActiveError responde el error de Deactivate/Reactivate: 404 si la
// entidad no existe, 400 para el resto (como antes).
func (h *WebHandler) setActiveError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, service.ErrNotFound) {
		http.Error(w, h.errMsg(r, err), http.StatusNotFound)
		return
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

func (h *WebHandler) tr(r *http.Request, key string) string {
	return i18n.T(transport.GetLocale(r.Context()), key)
}

// Index shows the dashboard
func (h *WebHandler) Index(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	brands, err := h.brandSvc.GetAll(ctx)
	brandCount := 0
	if err == nil {
		brandCount = len(brands)
	}

	taxes, err := h.taxSvc.GetAll(ctx)
	taxCount := 0
	if err == nil {
		taxCount = len(taxes)
	}

	h.render(w, r, "index.gohtml", map[string]any{
		"Section":    "dashboard",
		"TitleKey":   "nav.dashboard",
		"BrandCount": brandCount,
		"TaxCount":   taxCount,
	})
}

// BrandsList handles GET /brands
func (h *WebHandler) BrandsList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := strings.TrimSpace(r.URL.Query().Get("search"))

	var brands []*model.Brand
	var err error
	if query != "" {
		brands, err = h.brandSvc.SearchAllByName(ctx, query)
	} else {
		brands, err = h.brandSvc.GetAllIncludingInactive(ctx)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	isHtmx := r.Header.Get("HX-Request") != ""
	if isHtmx {
		h.renderPartial(w, r, "brand_table.gohtml", map[string]any{
			"Brands": brands,
		})
		return
	}

	h.render(w, r, "brands.gohtml", map[string]any{
		"Brands":   brands,
		"Section":  "brands",
		"TitleKey": "brand.title",
	})
}

// BrandsNewForm handles GET /brands/new
func (h *WebHandler) BrandsNewForm(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, r, "brand_form.gohtml", nil)
}

// BrandsCreate handles POST /brands/new
func (h *WebHandler) BrandsCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		h.renderPartial(w, r, "brand_form.gohtml", map[string]any{"Error": h.errMsg(r, err)})
		return
	}

	brand := &model.Brand{Name: r.FormValue("name")}
	if _, err := h.brandSvc.Create(ctx, brand); err != nil {
		h.renderPartial(w, r, "brand_form.gohtml", map[string]any{
			"Error": h.errMsg(r, err),
			"Brand": brand,
		})
		return
	}

	w.Header().Set("HX-Redirect", "/brands")
	w.WriteHeader(http.StatusOK)
}

// BrandsEditForm handles GET /brands/{id}/edit
func (h *WebHandler) BrandsEditForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	brand, err := h.brandSvc.GetByID(ctx, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	h.renderPartial(w, r, "brand_form.gohtml", map[string]any{
		"Brand": brand,
	})
}

// BrandsUpdate handles POST /brands/{id}/edit
func (h *WebHandler) BrandsUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		h.renderPartial(w, r, "brand_form.gohtml", map[string]any{"Error": h.tr(r, "error.invalid_id")})
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderPartial(w, r, "brand_form.gohtml", map[string]any{"Error": h.errMsg(r, err)})
		return
	}

	brand, err := h.brandSvc.GetByID(ctx, id)
	if err != nil {
		h.renderPartial(w, r, "brand_form.gohtml", map[string]any{"Error": h.errMsg(r, err)})
		return
	}
	brand.Name = r.FormValue("name")

	if _, err := h.brandSvc.Update(ctx, id, brand); err != nil {
		h.renderPartial(w, r, "brand_form.gohtml", map[string]any{
			"Error": h.errMsg(r, err),
			"Brand": brand,
		})
		return
	}

	w.Header().Set("HX-Redirect", "/brands")
	w.WriteHeader(http.StatusOK)
}

// BrandsDelete handles DELETE /brands/{id}
func (h *WebHandler) BrandsDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.brandSvc.Delete(ctx, id); err != nil {
		if errors.Is(err, service.ErrInUse) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	h.brandsTable(w, r)
}

// BrandsDeactivate handles PATCH /brands/{id}/deactivate
func (h *WebHandler) BrandsDeactivate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.brandSvc.Deactivate(ctx, id); err != nil {
		h.setActiveError(w, r, err)
		return
	}

	h.brandsTable(w, r)
}

// BrandsReactivate handles PATCH /brands/{id}/reactivate
func (h *WebHandler) BrandsReactivate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.brandSvc.Reactivate(ctx, id); err != nil {
		h.setActiveError(w, r, err)
		return
	}

	h.brandsTable(w, r)
}

func (h *WebHandler) brandsTable(w http.ResponseWriter, r *http.Request) {
	brands, err := h.brandSvc.GetAll(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPartial(w, r, "brand_table.gohtml", map[string]any{
		"Brands": brands,
	})
}

// ---- Tax handlers ----

// TaxesList handles GET /taxes
func (h *WebHandler) TaxesList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := strings.TrimSpace(r.URL.Query().Get("search"))

	var taxes []*model.Tax
	var err error
	if query != "" {
		taxes, err = h.taxSvc.SearchAllByName(ctx, query)
	} else {
		taxes, err = h.taxSvc.GetAllIncludingInactive(ctx)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	isHtmx := r.Header.Get("HX-Request") != ""
	if isHtmx {
		h.renderPartial(w, r, "tax_table.gohtml", map[string]any{
			"Taxes": taxes,
		})
		return
	}

	h.render(w, r, "taxes.gohtml", map[string]any{
		"Taxes":    taxes,
		"Section":  "taxes",
		"TitleKey": "tax.title",
	})
}

// TaxesNewForm handles GET /taxes/new
func (h *WebHandler) TaxesNewForm(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, r, "tax_form.gohtml", nil)
}

// TaxesCreate handles POST /taxes/new
func (h *WebHandler) TaxesCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		h.renderPartial(w, r, "tax_form.gohtml", map[string]any{"Error": h.errMsg(r, err)})
		return
	}

	rate, err := strconv.ParseFloat(r.FormValue("rate"), 64)
	if err != nil {
		h.renderPartial(w, r, "tax_form.gohtml", map[string]any{"Error": h.tr(r, "error.invalid_rate")})
		return
	}

	tax := &model.Tax{
		Name: r.FormValue("name"),
		Rate: decimal.NewFromFloat(rate),
	}
	if _, err := h.taxSvc.Create(ctx, tax); err != nil {
		h.renderPartial(w, r, "tax_form.gohtml", map[string]any{
			"Error": h.errMsg(r, err),
			"Tax":   tax,
		})
		return
	}

	w.Header().Set("HX-Redirect", "/taxes")
	w.WriteHeader(http.StatusOK)
}

// TaxesEditForm handles GET /taxes/{id}/edit
func (h *WebHandler) TaxesEditForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	tax, err := h.taxSvc.GetByID(ctx, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	h.renderPartial(w, r, "tax_form.gohtml", map[string]any{
		"Tax": tax,
	})
}

// TaxesUpdate handles POST /taxes/{id}/edit
func (h *WebHandler) TaxesUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		h.renderPartial(w, r, "tax_form.gohtml", map[string]any{"Error": h.tr(r, "error.invalid_id")})
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderPartial(w, r, "tax_form.gohtml", map[string]any{"Error": h.errMsg(r, err)})
		return
	}

	rate, err := strconv.ParseFloat(r.FormValue("rate"), 64)
	if err != nil {
		h.renderPartial(w, r, "tax_form.gohtml", map[string]any{"Error": h.tr(r, "error.invalid_rate")})
		return
	}

	tax, err := h.taxSvc.GetByID(ctx, id)
	if err != nil {
		h.renderPartial(w, r, "tax_form.gohtml", map[string]any{"Error": h.errMsg(r, err)})
		return
	}
	tax.Name = r.FormValue("name")
	tax.Rate = decimal.NewFromFloat(rate)

	if _, err := h.taxSvc.Update(ctx, id, tax); err != nil {
		h.renderPartial(w, r, "tax_form.gohtml", map[string]any{
			"Error": h.errMsg(r, err),
			"Tax":   tax,
		})
		return
	}

	w.Header().Set("HX-Redirect", "/taxes")
	w.WriteHeader(http.StatusOK)
}

// TaxesDelete handles DELETE /taxes/{id}
func (h *WebHandler) TaxesDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.taxSvc.Delete(ctx, id); err != nil {
		if errors.Is(err, service.ErrInUse) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	h.taxesTable(w, r)
}

// TaxesDeactivate handles PATCH /taxes/{id}/deactivate
func (h *WebHandler) TaxesDeactivate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.taxSvc.Deactivate(ctx, id); err != nil {
		h.setActiveError(w, r, err)
		return
	}

	h.taxesTable(w, r)
}

// TaxesReactivate handles PATCH /taxes/{id}/reactivate
func (h *WebHandler) TaxesReactivate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.taxSvc.Reactivate(ctx, id); err != nil {
		h.setActiveError(w, r, err)
		return
	}

	h.taxesTable(w, r)
}

func (h *WebHandler) taxesTable(w http.ResponseWriter, r *http.Request) {
	taxes, err := h.taxSvc.GetAll(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPartial(w, r, "tax_table.gohtml", map[string]any{
		"Taxes": taxes,
	})
}

// SuppliersList handles GET /suppliers
func (h *WebHandler) SuppliersList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := strings.TrimSpace(r.URL.Query().Get("search"))

	var suppliers []*model.Supplier
	var err error
	if query != "" {
		suppliers, err = h.supplierSvc.SearchAllByName(ctx, query)
	} else {
		suppliers, err = h.supplierSvc.GetAllIncludingInactive(ctx)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") != "" {
		h.renderPartial(w, r, "supplier_table.gohtml", map[string]any{
			"Suppliers": suppliers,
		})
		return
	}

	h.render(w, r, "suppliers.gohtml", map[string]any{
		"Suppliers": suppliers,
		"Section":   "suppliers",
		"TitleKey":  "supplier.title",
	})
}

// SuppliersNewForm handles GET /suppliers/new
func (h *WebHandler) SuppliersNewForm(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, r, "supplier_form.gohtml", nil)
}

// supplierFromForm copia los campos del formulario a s. La limpieza
// (espacios, CUIT sin guiones, email en minúscula) la hace Validate().
func supplierFromForm(r *http.Request, s *model.Supplier) {
	s.Name = r.FormValue("name")
	s.TaxIDNumber = r.FormValue("tax_id_number")
	s.Phone = r.FormValue("phone")
	s.Email = r.FormValue("email")
	s.PaymentTerms = r.FormValue("payment_terms")
}

// SuppliersCreate handles POST /suppliers/new
func (h *WebHandler) SuppliersCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		h.renderPartial(w, r, "supplier_form.gohtml", map[string]any{"Error": h.errMsg(r, err)})
		return
	}

	supplier := &model.Supplier{}
	supplierFromForm(r, supplier)
	if _, err := h.supplierSvc.Create(ctx, supplier); err != nil {
		h.renderPartial(w, r, "supplier_form.gohtml", map[string]any{
			"Error":    h.errMsg(r, err),
			"Supplier": supplier,
		})
		return
	}

	w.Header().Set("HX-Redirect", "/suppliers")
	w.WriteHeader(http.StatusOK)
}

// SuppliersEditForm handles GET /suppliers/{id}/edit
func (h *WebHandler) SuppliersEditForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	supplier, err := h.supplierSvc.GetByID(ctx, id)
	if err != nil {
		http.Error(w, h.errMsg(r, err), http.StatusNotFound)
		return
	}

	h.renderPartial(w, r, "supplier_form.gohtml", map[string]any{
		"Supplier": supplier,
	})
}

// SuppliersUpdate handles POST /suppliers/{id}/edit
func (h *WebHandler) SuppliersUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		h.renderPartial(w, r, "supplier_form.gohtml", map[string]any{"Error": h.tr(r, "error.invalid_id")})
		return
	}

	if err := r.ParseForm(); err != nil {
		h.renderPartial(w, r, "supplier_form.gohtml", map[string]any{"Error": h.errMsg(r, err)})
		return
	}

	// Se parte del proveedor guardado para no perder is_active.
	supplier, err := h.supplierSvc.GetByID(ctx, id)
	if err != nil {
		h.renderPartial(w, r, "supplier_form.gohtml", map[string]any{"Error": h.errMsg(r, err)})
		return
	}
	supplierFromForm(r, supplier)

	if _, err := h.supplierSvc.Update(ctx, id, supplier); err != nil {
		h.renderPartial(w, r, "supplier_form.gohtml", map[string]any{
			"Error":    h.errMsg(r, err),
			"Supplier": supplier,
		})
		return
	}

	w.Header().Set("HX-Redirect", "/suppliers")
	w.WriteHeader(http.StatusOK)
}

// SuppliersDelete handles DELETE /suppliers/{id}
func (h *WebHandler) SuppliersDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.supplierSvc.Delete(ctx, id); err != nil {
		switch {
		case errors.Is(err, service.ErrInUse):
			http.Error(w, h.errMsg(r, err), http.StatusConflict)
		case errors.Is(err, service.ErrNotFound):
			http.Error(w, h.errMsg(r, err), http.StatusNotFound)
		default:
			http.Error(w, err.Error(), http.StatusBadRequest)
		}
		return
	}

	h.suppliersTable(w, r)
}

// SuppliersDeactivate handles PATCH /suppliers/{id}/deactivate
func (h *WebHandler) SuppliersDeactivate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.supplierSvc.Deactivate(ctx, id); err != nil {
		h.setActiveError(w, r, err)
		return
	}

	h.suppliersTable(w, r)
}

// SuppliersReactivate handles PATCH /suppliers/{id}/reactivate
func (h *WebHandler) SuppliersReactivate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.supplierSvc.Reactivate(ctx, id); err != nil {
		h.setActiveError(w, r, err)
		return
	}

	h.suppliersTable(w, r)
}

// suppliersTable redibuja la tabla después de una acción. Incluye los
// inactivos, igual que SuppliersList: si no, al desactivar uno desaparecería
// de la lista y no se podría reactivar sin recargar la página.
func (h *WebHandler) suppliersTable(w http.ResponseWriter, r *http.Request) {
	suppliers, err := h.supplierSvc.GetAllIncludingInactive(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPartial(w, r, "supplier_table.gohtml", map[string]any{
		"Suppliers": suppliers,
	})
}

// SetLanguage handles POST /settings/language
func (h *WebHandler) SetLanguage(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	lang := r.FormValue("lang")
	if lang != "en" && lang != "es" {
		lang = "en"
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "lang",
		Value:    lang,
		Path:     "/",
		MaxAge:   365 * 24 * 3600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})

	w.Header().Set("HX-Refresh", "true")
	w.WriteHeader(http.StatusOK)
}