package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/i18n"
	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sales"
)

// Devoluciones (0028): solo el admin (permiso returns). Se busca la venta
// por número, se elige qué devolver y cómo vuelve la plata.

// ReturnsPage handles GET /returns[?number=N].
func (h *WebHandler) ReturnsPage(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Section": "returns", "TitleKey": "return.title"}
	if v := strings.TrimSpace(r.FormValue("number")); v != "" {
		data["Number"] = v
		n, err := strconv.Atoi(v)
		if err != nil {
			data["Error"] = h.tr(r, "error.not_found")
		} else if view, err := h.sales.FindForReturn(r.Context(), n); err != nil {
			data["Error"] = h.errMsg(r, err)
		} else {
			data["View"] = view
		}
	}
	h.renderReturns(w, r, data)
}

func (h *WebHandler) renderReturns(w http.ResponseWriter, r *http.Request, data map[string]any) {
	data["Methods"] = model.SaleMethods
	h.render(w, r, "returns.gohtml", data)
}

// ReturnsCreate handles POST /returns: sale_number, reason, por renglón
// qty_<id> y restock_<id> (on = vuelve a la góndola), y los pagos
// pay_method / pay_amount / pay_ref en paralelo.
func (h *WebHandler) ReturnsCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	data := map[string]any{"Section": "returns", "TitleKey": "return.title", "Number": r.FormValue("sale_number"),
		"Reason": r.FormValue("reason")}
	n, err := strconv.Atoi(r.FormValue("sale_number"))
	if err != nil {
		data["Error"] = h.tr(r, "error.not_found")
		h.renderReturns(w, r, data)
		return
	}
	view, err := h.sales.FindForReturn(ctx, n)
	if err != nil {
		data["Error"] = h.errMsg(r, err)
		h.renderReturns(w, r, data)
		return
	}
	data["View"] = view
	var lines []sales.ReturnLine
	for _, it := range view.Sale.Items {
		raw := strings.TrimSpace(r.PostForm.Get(fmt.Sprintf("qty_%d", it.ID)))
		if raw == "" || raw == "0" {
			continue
		}
		q, ok := parseFormDecimal(raw)
		if !ok {
			data["Error"] = h.errMsg(r, model.ErrReturnQuantity)
			h.renderReturns(w, r, data)
			return
		}
		lines = append(lines, sales.ReturnLine{SaleItemID: it.ID, Quantity: q,
			Restock: r.PostForm.Get(fmt.Sprintf("restock_%d", it.ID)) == "on"})
	}
	methods, amounts, refs := r.PostForm["pay_method"], r.PostForm["pay_amount"], r.PostForm["pay_ref"]
	var pays []*model.Payment
	for i := range methods {
		if i >= len(amounts) || strings.TrimSpace(amounts[i]) == "" {
			continue
		}
		a, ok := parseFormMoney(amounts[i])
		if !ok {
			data["Error"] = h.errMsg(r, model.ErrInvalidPaymentAmount)
			h.renderReturns(w, r, data)
			return
		}
		ref := ""
		if i < len(refs) {
			ref = refs[i]
		}
		pays = append(pays, &model.Payment{Method: methods[i], Amount: a, Reference: ref})
	}
	ret, err := h.sales.Return(ctx, transport.UserID(ctx), view.Sale.ID, r.FormValue("reason"), lines, pays)
	if err != nil {
		data["Error"] = h.posErrMsg(r, err)
		h.renderReturns(w, r, data)
		return
	}
	lang := transport.GetLocale(ctx)
	h.renderReturns(w, r, map[string]any{"Section": "returns", "TitleKey": "return.title",
		"Done": fmt.Sprintf(i18n.T(lang, "return.done"), view.Sale.SaleNumber, refundList(lang, ret.Payments))})
}
