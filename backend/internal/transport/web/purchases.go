package web

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/i18n"
	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
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
			res, rerr := h.purchases.Receive(r.Context(), transport.UserID(r.Context()), p)
			if rerr == nil {
				h.renderPartial(w, r, "purchase_received.gohtml", map[string]any{
					"Purchase":      res.Purchase,
					"Difference":    res.TotalDifference,
					"HasDifference": !res.TotalDifference.IsZero(),
					// Los precios se ponen en el detalle de la factura
					// (/purchases/{id}): ahí el resultado manda.
					"Products":  len(res.Suggestions),
					"Unstocked": h.unstockedIn(r.Context(), res.Purchase.Items),
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

// unstockedIn son los productos de la compra que se vendieron sin stock
// (modo arranque) y todavía no se contaron. Lo que llegó en la compra está
// bien; lo que falta saber es cuánto queda de la mercadería vieja. Conviene
// contarlo antes de acomodar lo nuevo, así no se mezclan. Si la lista falla,
// no hay aviso: la compra ya se guardó.
func (h *WebHandler) unstockedIn(ctx context.Context, items []*model.PurchaseItem) []model.UnstockedItem {
	if h.reports == nil || len(items) == 0 {
		return nil
	}
	inPurchase := make(map[int64]bool, len(items))
	for _, it := range items {
		inPurchase[it.ProductID] = true
	}
	all, err := h.reports.Unstocked(ctx)
	if err != nil {
		return nil
	}
	var out []model.UnstockedItem
	for _, it := range all {
		if inPurchase[it.ProductID] {
			out = append(out, it)
		}
	}
	return out
}
