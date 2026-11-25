package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/i18n"
	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/purchases"
)

// purchaseLine es un renglón del formulario tal como se escribió (texto), para
// volver a dibujarlo si algo falla. Error marca el renglón que falló.
type purchaseLine struct {
	ProductID, ProductQ, Quantity, UnitCost, BatchNumber, ExpiryDate string
	Batches                                                          bool
	Error                                                            bool
}

// PurchasesPage handles GET /purchases: el formulario vacío.
func (h *WebHandler) PurchasesPage(w http.ResponseWriter, r *http.Request) {
	data, err := h.purchaseFormData(r.Context(), 0)
	if err != nil {
		http.Error(w, h.errMsg(r, err), http.StatusInternalServerError)
		return
	}
	data["Section"] = "purchases"
	data["TitleKey"] = "purchase.title"
	h.render(w, r, "purchases.gohtml", data)
}

// purchaseFormData arma las opciones del formulario: solo proveedores activos.
func (h *WebHandler) purchaseFormData(ctx context.Context, supplierID int64) (map[string]any, error) {
	suppliers, err := h.supplierSvc.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	var current *int64
	if supplierID > 0 {
		current = &supplierID
	}
	return map[string]any{"Suppliers": supplierOptions(suppliers, current)}, nil
}

// PurchasesProductOptions handles GET /purchases/product-options: el buscador
// de producto de un renglón. Busca activos por nombre, sku o código de barras
// (sirve escanear). Los combos no se muestran: no se reciben (se reciben sus
// partes).
func (h *WebHandler) PurchasesProductOptions(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.FormValue("product_q"))
	found, err := h.productSvc.SearchByName(r.Context(), query)
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	products := make([]*model.Product, 0, len(found))
	for _, p := range found {
		if p.SaleType != model.SaleTypeComposite {
			products = append(products, p)
		}
	}
	h.renderPartial(w, r, "purchase_product_options.gohtml", map[string]any{"Products": products, "Query": query})
}

// PurchasesReceive handles POST /purchases: guarda la recepción y responde el
// resultado, o el formulario con lo escrito y el error.
func (h *WebHandler) PurchasesReceive(w http.ResponseWriter, r *http.Request) {
	err := r.ParseForm()
	supplierID, _ := strconv.ParseInt(r.FormValue("supplier_id"), 10, 64)
	lines := purchaseLinesFromForm(r)

	if err == nil {
		var p *model.Purchase
		p, err = purchaseFromForm(r, supplierID, lines)
		if err == nil {
			res, rerr := h.purchases.Receive(r.Context(), p)
			if rerr == nil {
				h.renderPartial(w, r, "purchase_received.gohtml", map[string]any{
					"Purchase":      res.Purchase,
					"Difference":    res.TotalDifference,
					"HasDifference": !res.TotalDifference.IsZero(),
					"Suggestions":   purchaseSuggestionRows(res.Suggestions),
				})
				return
			}
			err = rerr
		}
	}

	// Volver al formulario con lo escrito y el renglón marcado.
	data, ferr := h.purchaseFormData(r.Context(), supplierID)
	if ferr != nil {
		http.Error(w, h.errMsg(r, ferr), http.StatusInternalServerError)
		return
	}
	n := errorLine(err)
	if n > 0 && n <= len(lines) {
		lines[n-1].Error = true
	}
	data["VoucherNumber"] = r.FormValue("voucher_number")
	data["Total"] = r.FormValue("total")
	data["Lines"] = lines
	data["Error"] = h.purchaseErrMsg(r, err, n)
	h.renderPartial(w, r, "purchase_form.gohtml", data)
}

// purchaseLinesFromForm lee los renglones: cada campo llega como una lista
// (product_id, quantity...), una posición por renglón, en orden. Los
// renglones totalmente vacíos (agregados de más) se descartan.
func purchaseLinesFromForm(r *http.Request) []*purchaseLine {
	at := func(name string, i int) string {
		if v := r.Form[name]; i < len(v) {
			return strings.TrimSpace(v[i])
		}
		return ""
	}
	var lines []*purchaseLine
	for i := range r.Form["product_q"] {
		l := &purchaseLine{
			ProductID: at("product_id", i), ProductQ: at("product_q", i),
			Quantity: at("quantity", i), UnitCost: at("unit_cost", i),
			BatchNumber: at("batch_number", i), ExpiryDate: at("expiry_date", i),
			Batches: at("batches", i) == "1",
		}
		if l.ProductID == "" && l.ProductQ == "" && l.Quantity == "" && l.UnitCost == "" &&
			l.BatchNumber == "" && l.ExpiryDate == "" {
			continue
		}
		lines = append(lines, l)
	}
	return lines
}

// purchaseFromForm convierte el formulario en una compra. Un número mal
// escrito devuelve el error del modelo con su renglón, igual que Validate.
func purchaseFromForm(r *http.Request, supplierID int64, lines []*purchaseLine) (*model.Purchase, error) {
	total, ok := parseFormDecimal(r.FormValue("total"))
	if !ok {
		return nil, model.ErrInvalidPurchaseTotal
	}
	p := &model.Purchase{SupplierID: supplierID, VoucherNumber: r.FormValue("voucher_number"), Total: total}
	for i, l := range lines {
		productID, _ := strconv.ParseInt(l.ProductID, 10, 64)
		qty, ok := parseFormDecimal(l.Quantity)
		if !ok || l.Quantity == "" {
			return nil, fmt.Errorf("line %d: %w", i+1, model.ErrInvalidPurchaseQuantity)
		}
		cost, ok := parseFormDecimal(l.UnitCost)
		if !ok || l.UnitCost == "" {
			return nil, fmt.Errorf("line %d: %w", i+1, model.ErrInvalidCost)
		}
		p.Items = append(p.Items, &model.PurchaseItem{ProductID: productID, Quantity: qty, UnitCost: cost,
			BatchNumber: l.BatchNumber, ExpiryDate: l.ExpiryDate})
	}
	return p, nil
}

// lineRe encuentra "line N: " en un error de renglón (del modelo o del caso
// de uso). Puede venir después de otro prefijo ("validation failed: line 2:").
var lineRe = regexp.MustCompile(`\bline (\d+): `)

// errorLine es el número de renglón de un error (desde 1), o 0 si no es de un
// renglón.
func errorLine(err error) int {
	if err == nil {
		return 0
	}
	m := lineRe.FindStringSubmatch(err.Error())
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// purchaseErrMsg traduce el error y, si es de un renglón, dice cuál
// ("Renglón 2: El producto está desactivado.").
func (h *WebHandler) purchaseErrMsg(r *http.Request, err error, line int) string {
	msg := h.errMsg(r, err)
	if line > 0 {
		lang := transport.GetLocale(r.Context())
		msg = fmt.Sprintf(i18n.T(lang, "purchase.line_error"), line, msg)
	}
	return msg
}

// purchaseSuggestionRow es una fila de la tabla de precios después de recibir:
// la sugerencia del caso de uso y los datos de su celda de precio actual.
type purchaseSuggestionRow struct {
	purchases.Suggestion
	Cell map[string]any
}

func purchaseSuggestionRows(sugs []purchases.Suggestion) []purchaseSuggestionRow {
	rows := make([]purchaseSuggestionRow, len(sugs))
	for i, s := range sugs {
		rows[i] = purchaseSuggestionRow{Suggestion: s,
			Cell: priceCellData(s.ProductID, s.CurrentPrice, s.SuggestedPrice, false, "")}
	}
	return rows
}

// priceCellData arma los datos de purchase_price_cell.gohtml. Same dice si el
// sugerido ya es el precio vigente: entonces no hay botón (guardaría el mismo
// precio otra vez).
func priceCellData(productID int64, current, suggested *decimal.Decimal, saved bool, errMsg string) map[string]any {
	same := current != nil && suggested != nil && current.Equal(*suggested)
	return map[string]any{"ProductID": productID, "Current": current, "Suggested": suggested,
		"Same": same, "Saved": saved, "Error": errMsg}
}

// PurchasesUsePrice handles POST /purchases/prices/{id}: el "Usar como precio"
// de una fila del resultado. Guarda el sugerido como precio nuevo con el caso
// de uso de F-03 (prices.Set: precio y auditoría juntos) y responde la celda.
// Es otra ruta que POST /products/{id}/prices solo porque dibuja otra cosa.
func (h *WebHandler) PurchasesUsePrice(w http.ResponseWriter, r *http.Request) {
	productID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	raw := r.FormValue("unit_price")
	unitPrice, ok := parseFormDecimal(raw)
	if !ok || strings.TrimSpace(raw) == "" {
		err = model.ErrInvalidPrice
	} else {
		var saved *model.Price
		if saved, err = h.prices.Set(r.Context(), productID, unitPrice); err == nil {
			h.renderPartial(w, r, "purchase_price_cell.gohtml",
				priceCellData(productID, &saved.UnitPrice, &saved.UnitPrice, true, ""))
			return
		}
	}

	// No se guardó: la celda vuelve con el precio que tiene hoy, el botón
	// (para reintentar) y el error.
	if errors.Is(err, service.ErrNotFound) || errors.Is(err, service.ErrInvalidID) {
		h.actionError(w, r, err)
		return
	}
	prod, gerr := h.productSvc.GetByID(r.Context(), productID)
	if gerr != nil {
		h.actionError(w, r, gerr)
		return
	}
	var suggested *decimal.Decimal
	if ok && strings.TrimSpace(raw) != "" {
		suggested = &unitPrice
	}
	h.renderPartial(w, r, "purchase_price_cell.gohtml",
		priceCellData(productID, prod.CurrentPrice, suggested, false, h.errMsg(r, err)))
}
