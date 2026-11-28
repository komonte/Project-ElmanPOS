package web

import (
	"encoding/csv"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/komonte/Project-ElmanPOS/backend/internal/countcsv"
	"github.com/komonte/Project-ElmanPOS/backend/internal/i18n"
	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/adjustments"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/auth"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/combos"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/costs"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/createproduct"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/customers"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/prices"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/productcodes"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/purchases"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/reports"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sales"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sessions"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/settings"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/stockcounts"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/users"
	"github.com/shopspring/decimal"
)

type WebHandler struct {
	engine      *TemplateEngine
	brandSvc    *service.Service[model.Brand]
	taxSvc      *service.Service[model.Tax]
	supplierSvc *service.Service[model.Supplier]
	categorySvc *service.Service[model.Category]
	productSvc  *service.Service[model.Product]

	// Casos de uso (D-02).
	createProduct *createproduct.Service
	productCodes  *productcodes.Service
	combos        *combos.Service    // WithCombos
	customers     *customers.Service // WithCustomers
	prices        *prices.Service
	costs         *costs.Service
	purchases     *purchases.Service
	stockCounts   *stockcounts.Service
	adjustments   *adjustments.Service
	sessions      *sessions.Service
	sales         *sales.Service

	// Login (F-13): lo pone WithAuth. secureCookie: el server es HTTPS.
	auth         *auth.Service
	secureCookie bool

	// Página de usuarios (F-13): lo pone WithUsers.
	users *users.Service
	// Reportes (R-06, R-07): lo pone WithReports.
	reports *reports.Service
	// Configuración (modo arranque): lo pone WithSettings.
	settings *settings.Service
}

// WithAuth le da al handler el login (F-13). secureCookie va en true cuando
// el server sirve HTTPS: la cookie de la sesión solo viaja cifrada.
func (h *WebHandler) WithAuth(a *auth.Service, secureCookie bool) *WebHandler {
	h.auth, h.secureCookie = a, secureCookie
	return h
}

func NewWebHandler(engine *TemplateEngine, brandSvc *service.Service[model.Brand], taxSvc *service.Service[model.Tax], supplierSvc *service.Service[model.Supplier], categorySvc *service.Service[model.Category], productSvc *service.Service[model.Product], createProduct *createproduct.Service, productCodes *productcodes.Service, prices *prices.Service, costs *costs.Service, purchases *purchases.Service, stockCounts *stockcounts.Service, adjustments *adjustments.Service, sessions *sessions.Service, sales *sales.Service) *WebHandler {
	return &WebHandler{
		engine:        engine,
		brandSvc:      brandSvc,
		taxSvc:        taxSvc,
		supplierSvc:   supplierSvc,
		categorySvc:   categorySvc,
		productSvc:    productSvc,
		createProduct: createProduct,
		productCodes:  productCodes,
		prices:        prices,
		costs:         costs,
		purchases:     purchases,
		stockCounts:   stockCounts,
		adjustments:   adjustments,
		sessions:      sessions,
		sales:         sales,
	}
}

// adminListLimit es cuántas filas muestra una tabla de admin. Con miles de
// productos o marcas, la tabla trae solo estas y avisa cuántas hay en total.
const adminListLimit = 50

// adminList es lo que muestran las tablas de admin: activos e inactivos,
// filtrados por la búsqueda si hay una, los primeros adminListLimit, y
// cuántos hay en total (para el aviso "se muestran 50 de N"). La usan la
// carga de la página y las acciones de la tabla (borrar, desactivar,
// reactivar), que mandan la búsqueda actual con hx-include. Así, después de
// una acción la tabla no pierde el filtro ni esconde lo que se desactivó.
// FormValue lee tanto la query (GET, DELETE) como el cuerpo (PATCH).
func adminList[T any](r *http.Request, svc *service.Service[T]) ([]*T, int, error) {
	return svc.ListAll(r.Context(), r.FormValue("search"), adminListLimit)
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
	// Quién entró, para el menú (nil en el login).
	data["CurrentUser"] = transport.CurrentUser(r.Context())
	setCan(r, data)
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
	setCan(r, data)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if len(statusCode) > 0 {
		w.WriteHeader(statusCode[0])
	}
	if err := h.engine.Render(w, lang, partial, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// errorKeys traduce errores conocidos a claves de i18n. Se revisan en orden,
// así que los más específicos van primero: un CUIT repetido también es
// ErrAlreadyExists, pero el mensaje útil es el del CUIT.
var errorKeys = []struct {
	err error
	key string
}{
	{store.ErrDuplicateTaxID, "error.duplicate_tax_id"},
	{store.ErrDuplicateSKU, "error.duplicate_sku"},
	// Código repetido sin el dueño (dos altas a la vez). Con dueño, ver errMsg.
	{store.ErrDuplicateCode, "error.barcode_duplicate"},
	{store.ErrDuplicateName, "error.duplicate_name"},

	{model.ErrEmptyBrandName, "error.name_required"},
	{model.ErrBrandNameTooShort, "error.name_too_short"},
	{model.ErrBrandNameTooLong, "error.name_too_long"},

	{model.ErrEmptyTaxName, "error.name_required"},
	{model.ErrTaxNameTooShort, "error.name_too_short"},
	{model.ErrTaxNameTooLong, "error.name_too_long"},
	{model.ErrInvalidRate, "error.invalid_rate"},

	{model.ErrEmptySupplierName, "error.name_required"},
	{model.ErrSupplierNameTooShort, "error.name_too_short"},
	{model.ErrSupplierNameTooLong, "error.name_too_long"},
	{model.ErrInvalidSupplierTaxID, "error.invalid_tax_id"},
	{model.ErrInvalidSupplierPhone, "error.invalid_phone"},
	{model.ErrInvalidSupplierEmail, "error.invalid_email"},
	{model.ErrSupplierPaymentTermsTooLong, "error.payment_terms_too_long"},

	{model.ErrEmptyCategoryName, "error.name_required"},
	{model.ErrCategoryNameTooShort, "error.name_too_short"},
	{model.ErrCategoryNameTooLong, "error.name_too_long"},
	{model.ErrCategoryTaxRequired, "error.category_tax_required"},
	{model.ErrInvalidCategoryParent, "error.category_parent_invalid"},
	{store.ErrCategoryCycle, "error.category_cycle"},
	{store.ErrCategoryHasActiveChildren, "error.category_has_active_children"},
	{store.ErrCategoryParentInactive, "error.category_parent_inactive"},

	{model.ErrProductSKUTooLong, "error.product_sku_too_long"},
	{model.ErrEmptyProductName, "error.name_required"},
	{model.ErrProductNameTooShort, "error.name_too_short"},
	{model.ErrProductNameTooLong, "error.name_too_long"},
	{model.ErrProductDescriptionTooLong, "error.product_description_too_long"},
	{model.ErrProductCategoryRequired, "error.product_category_required"},
	// Solo pasan si alguien arma el formulario a mano: los <select> no los permiten.
	{model.ErrInvalidProductBrand, "error.product_choice_invalid"},
	{model.ErrInvalidProductSupplier, "error.product_choice_invalid"},
	{model.ErrInvalidSaleType, "error.product_choice_invalid"},
	{model.ErrInvalidMeasureUnit, "error.product_choice_invalid"},
	{model.ErrWeightNeedsKilogram, "error.product_weight_needs_kg"},
	{model.ErrCompositeBatches, "error.product_composite_batches"},
	{model.ErrInvalidReorderPoint, "error.product_reorder_point"},
	{model.ErrInvalidMaxStock, "error.product_max_stock"},
	{model.ErrInvalidMargin, "error.product_margin"},
	{model.ErrInvalidShelfLife, "error.product_shelf_life"},
	{store.ErrProductBrandInactive, "error.product_brand_inactive"},
	{store.ErrProductCategoryInactive, "error.product_category_inactive"},
	{store.ErrProductSupplierInactive, "error.product_supplier_inactive"},
	{store.ErrProductHasHistory, "error.product_has_history"},

	{model.ErrEmptyBarcode, "error.barcode_required"},
	{model.ErrBarcodeNotDigits, "error.barcode_not_digits"},
	{model.ErrBarcodeTooLong, "error.barcode_too_long"},
	{model.ErrBarcodeAllZeros, "error.barcode_all_zeros"},

	{model.ErrInvalidPrice, "error.price_invalid"},
	{prices.ErrProductInactive, "error.price_product_inactive"},

	{model.ErrInvalidCost, "error.cost_invalid"},
	{costs.ErrProductInactive, "error.cost_product_inactive"},

	// Recepción de compras (F-06). Los de un renglón se muestran con
	// "Renglón N:" adelante (purchaseErrMsg).
	{model.ErrPurchaseSupplierMissing, "error.purchase_supplier_missing"},
	{model.ErrPurchaseVoucherTooLong, "error.purchase_voucher_too_long"},
	{model.ErrInvalidPurchaseTotal, "error.purchase_total_invalid"},
	{model.ErrPurchaseNoItems, "error.purchase_no_items"},
	{model.ErrPurchaseItemProduct, "error.purchase_item_product"},
	{model.ErrInvalidPurchaseQuantity, "error.purchase_quantity_invalid"},
	{model.ErrBatchNumberTooLong, "error.batch_number_too_long"},
	{model.ErrInvalidExpiryDate, "error.expiry_invalid"},
	{purchases.ErrUnknownSupplier, "error.supplier_unknown"},
	{purchases.ErrSupplierInactive, "error.supplier_inactive"},
	{purchases.ErrUnknownProduct, "error.product_unknown"},
	{purchases.ErrProductInactive, "error.purchase_product_inactive"},
	{purchases.ErrCompositeProduct, "error.purchase_composite"},
	{purchases.ErrBatchNotAllowed, "error.batch_not_allowed"},
	{errLineProductInactive, "error.line_product_inactive"},
	{errLineComposite, "error.line_composite"},
	// Conteo de stock (F-09).
	{model.ErrStockCountEntryProduct, "error.count_product_missing"},
	{model.ErrStockCountSectorEmpty, "error.count_sector_required"},
	{model.ErrStockCountSectorLong, "error.count_sector_too_long"},
	{model.ErrInvalidCountedQuantity, "error.count_quantity_invalid"},
	{stockcounts.ErrUnknownProduct, "error.product_unknown"},
	{stockcounts.ErrProductInactive, "error.line_product_inactive"},
	{stockcounts.ErrCompositeProduct, "error.line_composite"},
	{stockcounts.ErrExpiryNotAllowed, "error.batch_not_allowed"},
	{stockcounts.ErrNegativeStock, "error.count_negative_stock"},
	{store.ErrCountDraftExists, "error.count_draft_exists"},
	{store.ErrCountNotDraft, "error.count_not_draft"},
	{store.ErrNegativeStock, "error.count_negative_stock"},
	// Salidas conocidas (F-09b). La falta de stock dice cuánto hay (errText).
	{model.ErrStockAdjustmentProduct, "error.count_product_missing"},
	{model.ErrInvalidAdjustmentReason, "error.adjustment_reason_invalid"},
	{model.ErrInvalidAdjustmentQuantity, "error.adjustment_quantity_invalid"},
	{model.ErrAdjustmentNotesTooLong, "error.adjustment_notes_too_long"},
	{model.ErrAdjustmentNotesRequired, "error.adjustment_notes_required"},
	{model.ErrInvalidAdjustmentBatch, "error.adjustment_batch_invalid"},
	{adjustments.ErrUnknownProduct, "error.product_unknown"},
	{adjustments.ErrProductInactive, "error.line_product_inactive"},
	{adjustments.ErrCompositeProduct, "error.line_composite"},
	{adjustments.ErrBatchNotAllowed, "error.adjustment_batch_invalid"},
	{adjustments.ErrUnknownBatch, "error.adjustment_batch_invalid"},
	// Importar un conteo desde una planilla (F-09). La columna que falta y el
	// tamaño los arma importErrMsg.
	{countcsv.ErrEmptyFile, "error.import_empty"},
	{countcsv.ErrCodeMissing, "error.import_code_missing"},
	{countcsv.ErrCodeScientific, "error.import_code_scientific"},
	{countcsv.ErrQuantityMissing, "error.import_quantity_missing"},
	{countcsv.ErrQuantityInvalid, "error.import_quantity_invalid"},
	{countcsv.ErrCostInvalid, "error.import_cost_invalid"},
	{countcsv.ErrExpiryInvalid, "error.import_expiry_invalid"},
	{csv.ErrQuote, "error.import_bad_row"},
	{csv.ErrBareQuote, "error.import_bad_row"},
	{stockcounts.ErrCodeNotFound, "error.import_code_not_found"},
	{stockcounts.ErrImportSectorRequired, "error.count_sector_required"},
	// Caja (F-11).
	{model.ErrInvalidOpeningAmount, "error.cash_opening_invalid"},
	{sessions.ErrNoRegister, "error.cash_no_register"},
	{store.ErrSessionAlreadyOpen, "error.cash_already_open"},
	{store.ErrSeveralActiveRegisters, "error.cash_several_registers"},
	// Cierre (F-12).
	{store.ErrSessionNotOpen, "error.cash_session_not_open"},
	{model.ErrInvalidDeclaredAmount, "error.cash_declared_invalid"},
	{model.ErrInvalidCashRounding, "error.cash_rounding_invalid"},
	// Clientes (fiado).
	{model.ErrCustomerNameRequired, "error.name_required"},
	{model.ErrCustomerNameTooShort, "error.name_too_short"},
	{model.ErrCustomerNameTooLong, "error.name_too_long"},
	{model.ErrCustomerPhoneTooLong, "error.customer_phone_too_long"},
	{model.ErrCustomerNotesTooLong, "error.customer_notes_too_long"},
	{customers.ErrNameTaken, "error.customer_name_taken"},
	// Componentes de un combo (F-05). En la caja, un componente que ya no
	// sirve dice eso, no el error de la pantalla de componentes: va primero.
	{sales.ErrComboComponent, "error.sale_combo_component"},
	{model.ErrComponentProduct, "error.component_product"},
	{model.ErrComponentSelf, "error.component_self"},
	{model.ErrInvalidComponentQty, "error.component_quantity"},
	{model.ErrComponentPackNotCombo, "error.component_pack_not_combo"},
	{model.ErrComponentIsCombo, "error.component_is_combo"},
	{model.ErrComponentOpenPrice, "error.component_open_price"},
	{model.ErrComponentInactive, "error.component_inactive"},
	{combos.ErrComponentUnknown, "error.product_unknown"},
	// Anular (F-08).
	{sales.ErrSaleOtherSession, "error.void_other_session"},
	{model.ErrSaleAlreadyVoided, "error.void_already_voided"},
	{model.ErrVoidReasonRequired, "error.void_reason_required"},
	{model.ErrVoidReasonTooLong, "error.void_reason_too_long"},
	// Venta (F-07, F-10). Los de un renglón llevan el producto adelante y la
	// falta de stock dice cuánto hay (posErrMsg).
	{sales.ErrRegisterClosed, "error.sale_register_closed"},
	{sales.ErrUnknownProduct, "error.product_unknown"},
	{sales.ErrProductInactive, "error.line_product_inactive"},
	{sales.ErrComboEmpty, "error.sale_combo_empty"},
	{sales.ErrNoPrice, "error.sale_no_price"},
	{model.ErrSaleNoItems, "error.sale_no_items"},
	{model.ErrSaleItemProduct, "error.sale_no_items"},
	{model.ErrSaleDuplicateProduct, "error.sale_duplicate_product"},
	{model.ErrInvalidSaleQuantity, "error.sale_quantity_invalid"},
	{model.ErrSaleQuantityNotWhole, "error.sale_quantity_not_whole"},
	// Conteos, salidas y compras: los productos por unidad, enteros.
	{model.ErrQuantityNotWhole, "error.quantity_not_whole"},
	{sales.ErrOpenPriceMissing, "error.open_price_missing"},
	// Fiado.
	{model.ErrSaleNeedsCustomer, "error.sale_needs_customer"},
	{model.ErrSaleTwoAccountPayments, "error.sale_two_account"},
	{sales.ErrCustomerUnavailable, "error.sale_customer_unavailable"},
	{prices.ErrOpenPriceKind, "error.open_price_kind"},
	{model.ErrSaleWeightTooLarge, "error.sale_weight_too_large"},
	{model.ErrSaleLineTotalZero, "error.sale_line_zero"},
	{model.ErrSaleTotalTooLarge, "error.sale_total_too_large"},
	{model.ErrSaleNoPayments, "error.sale_no_payments"},
	{model.ErrInvalidPaymentMethod, "error.sale_payment_method"},
	{model.ErrInvalidPaymentAmount, "error.sale_payment_amount"},
	{model.ErrPaymentReferenceTooLong, "error.sale_payment_reference"},
	{model.ErrSaleTwoCashPayments, "error.sale_two_cash"},
	{model.ErrPaymentsShort, "error.sale_payments_short"},
	{model.ErrPaymentsOverTotal, "error.sale_payments_over"},
	// Usuarios (F-13). El usuario repetido va antes que ErrAlreadyExists.
	{store.ErrDuplicateUsername, "error.user_duplicate"},
	{model.ErrInvalidUsername, "error.user_username_invalid"},
	{model.ErrEmptyUserName, "error.name_required"},
	{model.ErrUserNameTooShort, "error.name_too_short"},
	{model.ErrUserNameTooLong, "error.name_too_long"},
	{model.ErrInvalidUserRole, "error.user_role_invalid"},
	{model.ErrPasswordTooShort, "error.password_too_short"},
	{model.ErrPasswordTooLong, "error.password_too_long"},
	{errPasswordMismatch, "error.password_mismatch"},
	{users.ErrSelfDeactivate, "error.user_self_deactivate"},
	{users.ErrSelfRole, "error.user_self_role"},
	{users.ErrLastAdmin, "error.user_last_admin"},
	// Reportes (R-06, R-07): el período.
	{model.ErrInvalidPeriodDate, "error.period_date"},
	{model.ErrPeriodReversed, "error.period_reversed"},
	{model.ErrPeriodTooLong, "error.period_too_long"},
	{store.ErrNoActiveStore, "error.no_active_store"},
	{store.ErrSeveralActiveStores, "error.several_active_stores"},
}

// errMsg devuelve el mensaje para el usuario, en su idioma. Un error que no
// está en la lista (ej. la DB caída) se loguea y se muestra uno genérico.
func (h *WebHandler) errMsg(r *http.Request, err error) string {
	lang := transport.GetLocale(r.Context())
	msg := h.errText(r, lang, err)
	// Un error de la categoría nueva del alta se aclara: el producto también
	// tiene "nombre", y "Ya hay uno con ese nombre" sería ambiguo.
	if errors.Is(err, createproduct.ErrNewCategory) {
		msg = fmt.Sprintf(i18n.T(lang, "error.new_category"), msg)
	}
	return msg
}

// errText es el mensaje de un error, sin aclarar de qué parte del formulario es.
func (h *WebHandler) errText(r *http.Request, lang string, err error) string {
	// El código repetido dice de qué producto es: el mensaje lleva datos.
	var taken *productcodes.CodeTakenError
	if errors.As(err, &taken) {
		return fmt.Sprintf(i18n.T(lang, "error.barcode_taken"), taken.ProductSKU, taken.ProductName)
	}
	var short *adjustments.NotEnoughStockError
	if errors.As(err, &short) {
		return fmt.Sprintf(i18n.T(lang, "error.adjustment_not_enough"), formatQuantity(lang, short.Available))
	}
	var brandInactive *createproduct.BrandInactiveError
	if errors.As(err, &brandInactive) {
		return fmt.Sprintf(i18n.T(lang, "error.brand_exists_inactive"), brandInactive.Name)
	}
	for _, e := range errorKeys {
		if errors.Is(err, e.err) {
			return i18n.T(lang, e.key)
		}
	}
	switch {
	case errors.Is(err, service.ErrAlreadyExists):
		return i18n.T(lang, "error.already_exists")
	case errors.Is(err, service.ErrNotFound):
		return i18n.T(lang, "error.not_found")
	case errors.Is(err, service.ErrInUse):
		return i18n.T(lang, "error.in_use")
	case errors.Is(err, service.ErrInvalidReference):
		return i18n.T(lang, "error.invalid_reference")
	case errors.Is(err, service.ErrInvalidID):
		return i18n.T(lang, "error.invalid_id")
	default:
		slog.Error("web request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		return i18n.T(lang, "error.unexpected")
	}
}

// actionError responde el error de una acción de tabla (borrar, desactivar,
// reactivar). El layout muestra el texto de la respuesta en un aviso, así que
// va traducido. Un error inesperado (ej. la DB caída) se loguea y el usuario
// ve un mensaje genérico, no el detalle interno.
func (h *WebHandler) actionError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrInUse),
		// Reglas como las del árbol de categorías (hijas activas, padre inactivo).
		errors.Is(err, service.ErrConflict):
		http.Error(w, h.errMsg(r, err), http.StatusConflict)
	case errors.Is(err, service.ErrNotFound):
		http.Error(w, h.errMsg(r, err), http.StatusNotFound)
	case errors.Is(err, service.ErrInvalidID):
		http.Error(w, h.errMsg(r, err), http.StatusBadRequest)
	default:
		slog.Error("web action failed", "method", r.Method, "path", r.URL.Path, "error", err)
		http.Error(w, h.tr(r, "error.unexpected"), http.StatusInternalServerError)
	}
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

	suppliers, err := h.supplierSvc.GetAll(ctx)
	supplierCount := 0
	if err == nil {
		supplierCount = len(suppliers)
	}

	categories, err := h.categorySvc.GetAll(ctx)
	categoryCount := 0
	if err == nil {
		categoryCount = len(categories)
	}

	data := map[string]any{
		"Section":       "dashboard",
		"TitleKey":      "nav.dashboard",
		"BrandCount":    brandCount,
		"TaxCount":      taxCount,
		"SupplierCount": supplierCount,
		"CategoryCount": categoryCount,
	}
	// Precios pendientes (F-06): lo que no se puede vender por falta de
	// precio y las facturas sin cotizar, para quien pone precios.
	if can(r, model.PermPrices) {
		if p, err := h.purchases.Pending(ctx); err == nil {
			data["Pending"] = p
		}
	}
	// Modo arranque: que se note que está prendido y, para quien cuenta,
	// cuántos productos vendidos sin stock faltan contar.
	if h.settings != nil {
		if st, err := h.settings.Get(ctx); err == nil && st.StartupMode {
			data["StartupMode"] = true
		}
	}
	if can(r, model.PermStock) && h.reports != nil {
		if items, err := h.reports.Unstocked(ctx); err == nil {
			data["UnstockedCount"] = len(items)
		}
	}
	// Códigos cobrados como artículo sin registrar: para quien da de alta.
	if can(r, model.PermCatalog) && h.reports != nil {
		if codes, err := h.reports.UnregisteredCodes(ctx); err == nil {
			data["UnregisteredCount"] = len(codes)
		}
	}
	h.render(w, r, "index.gohtml", data)
}

// BrandsList handles GET /brands
func (h *WebHandler) BrandsList(w http.ResponseWriter, r *http.Request) {
	brands, total, err := adminList(r, h.brandSvc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	isHtmx := r.Header.Get("HX-Request") != ""
	if isHtmx {
		h.renderPartial(w, r, "brand_table.gohtml", map[string]any{
			"Brands": brands, "Shown": len(brands), "Total": total,
		})
		return
	}

	h.render(w, r, "brands.gohtml", map[string]any{
		"Brands": brands, "Shown": len(brands), "Total": total,
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
		h.actionError(w, r, err)
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
		h.actionError(w, r, err)
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
		h.actionError(w, r, err)
		return
	}

	h.brandsTable(w, r)
}

func (h *WebHandler) brandsTable(w http.ResponseWriter, r *http.Request) {
	brands, total, err := adminList(r, h.brandSvc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPartial(w, r, "brand_table.gohtml", map[string]any{
		"Brands": brands, "Shown": len(brands), "Total": total,
	})
}

// ---- Tax handlers ----

// TaxesList handles GET /taxes
func (h *WebHandler) TaxesList(w http.ResponseWriter, r *http.Request) {
	taxes, total, err := adminList(r, h.taxSvc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	isHtmx := r.Header.Get("HX-Request") != ""
	if isHtmx {
		h.renderPartial(w, r, "tax_table.gohtml", map[string]any{
			"Taxes": taxes, "Shown": len(taxes), "Total": total,
		})
		return
	}

	h.render(w, r, "taxes.gohtml", map[string]any{
		"Taxes": taxes, "Shown": len(taxes), "Total": total,
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
		h.actionError(w, r, err)
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
		h.actionError(w, r, err)
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
		h.actionError(w, r, err)
		return
	}

	h.taxesTable(w, r)
}

func (h *WebHandler) taxesTable(w http.ResponseWriter, r *http.Request) {
	taxes, total, err := adminList(r, h.taxSvc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPartial(w, r, "tax_table.gohtml", map[string]any{
		"Taxes": taxes, "Shown": len(taxes), "Total": total,
	})
}

// SuppliersList handles GET /suppliers
func (h *WebHandler) SuppliersList(w http.ResponseWriter, r *http.Request) {
	suppliers, total, err := adminList(r, h.supplierSvc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") != "" {
		h.renderPartial(w, r, "supplier_table.gohtml", map[string]any{
			"Suppliers": suppliers, "Shown": len(suppliers), "Total": total,
		})
		return
	}

	h.render(w, r, "suppliers.gohtml", map[string]any{
		"Suppliers": suppliers, "Shown": len(suppliers), "Total": total,
		"Section":  "suppliers",
		"TitleKey": "supplier.title",
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
		h.actionError(w, r, err)
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
		h.actionError(w, r, err)
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
		h.actionError(w, r, err)
		return
	}

	h.suppliersTable(w, r)
}

// suppliersTable redibuja la tabla después de una acción (ver adminList).
func (h *WebHandler) suppliersTable(w http.ResponseWriter, r *http.Request) {
	suppliers, total, err := adminList(r, h.supplierSvc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.renderPartial(w, r, "supplier_table.gohtml", map[string]any{
		"Suppliers": suppliers, "Shown": len(suppliers), "Total": total,
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
