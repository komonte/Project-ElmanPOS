package web

import (
	"errors"
	"fmt"
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
	engine *TemplateEngine
	brandSvc *service.Service[model.Brand]
	taxSvc   *service.Service[model.Tax]
}

func NewWebHandler(engine *TemplateEngine, brandSvc *service.Service[model.Brand], taxSvc *service.Service[model.Tax]) *WebHandler {
	return &WebHandler{
		engine:   engine,
		brandSvc: brandSvc,
		taxSvc:   taxSvc,
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

func (h *WebHandler) renderPartial(w http.ResponseWriter, r *http.Request, partial string, data map[string]any) {
	lang := transport.GetLocale(r.Context())
	if data == nil {
		data = make(map[string]any)
	}
	data["Lang"] = lang
	data["T"] = func(key string) string { return i18n.T(lang, key) }

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.engine.Render(w, lang, partial, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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

	h.render(w, r, "templates/pages/index.gohtml", map[string]any{
		"Section":   "dashboard",
		"TitleKey":  "nav.dashboard",
		"BrandCount": brandCount,
		"TaxCount":   taxCount,
	})
}

// BrandsList handles GET /brands (full page or partial for htmx search)
func (h *WebHandler) BrandsList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := strings.TrimSpace(r.URL.Query().Get("search"))

	var brands []*model.Brand
	var err error
	if query != "" {
		brands, err = h.brandSvc.SearchByName(ctx, query)
	} else {
		brands, err = h.brandSvc.GetAll(ctx)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// If it's an htmx request, only return the table partial
	isHtmx := r.Header.Get("HX-Request") != ""
	if isHtmx {
		h.renderPartial(w, r, "templates/pages/brands.gohtml", map[string]any{
			"Brands":  brands,
			"Section": "brands",
		})
		return
	}

	h.render(w, r, "templates/pages/brands.gohtml", map[string]any{
		"Brands":  brands,
		"Section": "brands",
		"TitleKey": "brand.title",
	})
}

// BrandsNewForm handles GET /brands/new (form partial)
func (h *WebHandler) BrandsNewForm(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, r, "templates/partials/brand_form.gohtml", map[string]any{
		"TitleKey": "brand.create",
	})
}

// BrandsCreate handles POST /brands/new
func (h *WebHandler) BrandsCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	brand := &model.Brand{Name: r.FormValue("name")}
	if _, err := h.brandSvc.Create(ctx, brand); err != nil {
		h.brandsTableError(w, r, err)
		return
	}

	h.brandsTable(w, r)
}

// BrandsEditForm handles GET /brands/{id}/edit (form partial)
func (h *WebHandler) BrandsEditForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseID(r.URL.Path, "/brands/", "/edit")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	brand, err := h.brandSvc.GetByID(ctx, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	h.renderPartial(w, r, "templates/partials/brand_form.gohtml", map[string]any{
		"Brand": brand,
	})
}

// BrandsUpdate handles POST /brands/{id}/edit
func (h *WebHandler) BrandsUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseID(r.URL.Path, "/brands/", "/edit")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	brand := &model.Brand{Name: r.FormValue("name")}
	if _, err := h.brandSvc.Update(ctx, id, brand); err != nil {
		h.brandsTableError(w, r, err)
		return
	}

	h.brandsTable(w, r)
}

// BrandsDelete handles DELETE /brands/{id}
func (h *WebHandler) BrandsDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseID(r.URL.Path, "/brands/", "")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.brandSvc.Delete(ctx, id); err != nil {
		h.brandsTableError(w, r, err)
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
	h.renderPartial(w, r, "templates/pages/brands.gohtml", map[string]any{
		"Brands": brands,
	})
}

func (h *WebHandler) brandsTableError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, service.ErrAlreadyExists) {
		w.Header().Set("HX-Reswap", "none")
		lang := transport.GetLocale(r.Context())
		msg := i18n.T(lang, "brand.name") + " " + i18n.T(lang, "common."+extractErrorSuffix(err))
		w.Header().Set("HX-Trigger", fmt.Sprintf(`{"show-error":"%s"}`, msg))
		w.WriteHeader(http.StatusConflict)
		return
	}
	if errors.Is(err, service.ErrNotFound) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if errors.Is(err, service.ErrInUse) {
		w.WriteHeader(http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusBadRequest)
}

// ---- Tax handlers ----

// TaxesList handles GET /taxes
func (h *WebHandler) TaxesList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	query := strings.TrimSpace(r.URL.Query().Get("search"))

	var taxes []*model.Tax
	var err error
	if query != "" {
		taxes, err = h.taxSvc.SearchByName(ctx, query)
	} else {
		taxes, err = h.taxSvc.GetAll(ctx)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	isHtmx := r.Header.Get("HX-Request") != ""
	if isHtmx {
		h.renderPartial(w, r, "templates/pages/taxes.gohtml", map[string]any{
			"Taxes": taxes,
		})
		return
	}

	h.render(w, r, "templates/pages/taxes.gohtml", map[string]any{
		"Taxes":    taxes,
		"Section":  "taxes",
		"TitleKey": "tax.title",
	})
}

// TaxesNewForm handles GET /taxes/new
func (h *WebHandler) TaxesNewForm(w http.ResponseWriter, r *http.Request) {
	h.renderPartial(w, r, "templates/partials/tax_form.gohtml", nil)
}

// TaxesCreate handles POST /taxes/new
func (h *WebHandler) TaxesCreate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rate, err := strconv.ParseFloat(r.FormValue("rate"), 64)
	if err != nil {
		http.Error(w, "invalid rate", http.StatusBadRequest)
		return
	}

	tax := &model.Tax{
		Name: r.FormValue("name"),
		Rate: shopspringDecimalFromFloat(rate),
	}
	if _, err := h.taxSvc.Create(ctx, tax); err != nil {
		h.taxesTableError(w, r, err)
		return
	}

	h.taxesTable(w, r)
}

// TaxesEditForm handles GET /taxes/{id}/edit
func (h *WebHandler) TaxesEditForm(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseID(r.URL.Path, "/taxes/", "/edit")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	tax, err := h.taxSvc.GetByID(ctx, id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	h.renderPartial(w, r, "templates/partials/tax_form.gohtml", map[string]any{
		"Tax": tax,
	})
}

// TaxesUpdate handles POST /taxes/{id}/edit
func (h *WebHandler) TaxesUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseID(r.URL.Path, "/taxes/", "/edit")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	rate, err := strconv.ParseFloat(r.FormValue("rate"), 64)
	if err != nil {
		http.Error(w, "invalid rate", http.StatusBadRequest)
		return
	}

	tax := &model.Tax{
		Name: r.FormValue("name"),
		Rate: shopspringDecimalFromFloat(rate),
	}
	if _, err := h.taxSvc.Update(ctx, id, tax); err != nil {
		h.taxesTableError(w, r, err)
		return
	}

	h.taxesTable(w, r)
}

// TaxesDelete handles DELETE /taxes/{id}
func (h *WebHandler) TaxesDelete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, err := parseID(r.URL.Path, "/taxes/", "")
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	if err := h.taxSvc.Delete(ctx, id); err != nil {
		h.taxesTableError(w, r, err)
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
	h.renderPartial(w, r, "templates/pages/taxes.gohtml", map[string]any{
		"Taxes": taxes,
	})
}

func (h *WebHandler) taxesTableError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, service.ErrAlreadyExists) {
		w.WriteHeader(http.StatusConflict)
		return
	}
	if errors.Is(err, service.ErrNotFound) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if errors.Is(err, service.ErrInUse) {
		w.WriteHeader(http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusBadRequest)
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

// ---- helpers ----

func parseID(path, prefix, suffix string) (int64, error) {
	s := strings.TrimPrefix(path, prefix)
	s = strings.TrimSuffix(s, suffix)
	return strconv.ParseInt(s, 10, 64)
}

func shopspringDecimalFromFloat(f float64) decimal.Decimal {
	// We need to import shopspring/decimal properly
	return decimal.NewFromFloat(f)
}

func extractErrorSuffix(err error) string {
	// Map common service errors to translation keys
	if errors.Is(err, service.ErrAlreadyExists) {
		return "already_exists"
	}
	return "error"
}