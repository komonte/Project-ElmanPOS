package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
)

// Salidas conocidas (F-09b): consumo propio, rotura, vencido u otra. Una por
// envío, con el buscador compartido (picker.go), y abajo las últimas.

// adjustmentRecentLimit es cuántas salidas muestra la lista de abajo.
const adjustmentRecentLimit = 50

// adjustmentForm es lo que se escribió, para volver a dibujarlo si algo
// falla. El motivo queda elegido también después de anotar: muchas veces se
// anotan varias del mismo motivo seguidas.
type adjustmentForm struct {
	ProductID, ProductQ, Quantity, Reason, BatchID, Notes string
}

// adjustmentLast es lo último que se anotó, para confirmarlo en pantalla.
type adjustmentLast struct {
	Name, Reason string
	Quantity     decimal.Decimal
	InOpenCount  bool
}

// AdjustmentsPage handles GET /adjustments.
func (h *WebHandler) AdjustmentsPage(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Section": "adjustments", "TitleKey": "adjustment.title"}
	form, err := h.adjustmentFormData(r, adjustmentForm{Reason: model.ReasonOwnConsumption}, nil, "")
	if err != nil {
		data["Error"] = h.errMsg(r, err)
	}
	data["Form"] = form
	recent, err := h.adjustments.Recent(r.Context(), adjustmentRecentLimit)
	if err != nil {
		data["Error"] = h.errMsg(r, err)
	}
	data["Recent"] = map[string]any{"Adjustments": recent}
	h.render(w, r, "adjustments.gohtml", data)
}

// AdjustmentsRecord handles POST /adjustments: anota la salida y responde el
// formulario listo para la siguiente (y la lista, fuera de lugar). Con error,
// el formulario vuelve con lo escrito y el mensaje.
func (h *WebHandler) AdjustmentsRecord(w http.ResponseWriter, r *http.Request) {
	form := adjustmentForm{
		ProductID: strings.TrimSpace(r.FormValue("product_id")),
		ProductQ:  strings.TrimSpace(r.FormValue("product_q")),
		Quantity:  strings.TrimSpace(r.FormValue("quantity")),
		Reason:    strings.TrimSpace(r.FormValue("reason")),
		BatchID:   strings.TrimSpace(r.FormValue("batch_id")),
		Notes:     strings.TrimSpace(r.FormValue("notes")),
	}

	var last *adjustmentLast
	a, err := adjustmentFromForm(form)
	if err == nil {
		res, rerr := h.adjustments.Record(r.Context(), transport.UserID(r.Context()), a)
		if rerr == nil {
			last = &adjustmentLast{Name: form.ProductQ, Reason: a.Reason, Quantity: a.Quantity,
				InOpenCount: res.InOpenCount}
			form = adjustmentForm{Reason: a.Reason}
		}
		err = rerr
	}
	errMsg := ""
	if err != nil {
		errMsg = h.errMsg(r, err)
	}
	data, ferr := h.adjustmentFormData(r, form, last, errMsg)
	if ferr != nil {
		h.actionError(w, r, ferr)
		return
	}
	h.renderPartial(w, r, "adjustment_form.gohtml", data)
	if last != nil {
		recent, err := h.adjustments.Recent(r.Context(), adjustmentRecentLimit)
		if err != nil {
			return // la salida se anotó; la lista se ve al recargar
		}
		h.renderPartial(w, r, "adjustment_recent.gohtml", map[string]any{"Adjustments": recent, "OOB": true})
	}
}

// adjustmentFromForm convierte el formulario. Sin producto elegido, o con
// una cantidad vacía o mal escrita, el error del modelo (como Validate).
func adjustmentFromForm(f adjustmentForm) (*model.StockAdjustment, error) {
	productID, _ := strconv.ParseInt(f.ProductID, 10, 64)
	if productID <= 0 {
		return nil, model.ErrStockAdjustmentProduct
	}
	qty, ok := parseFormDecimal(f.Quantity)
	if !ok || f.Quantity == "" {
		return nil, model.ErrInvalidAdjustmentQuantity
	}
	a := &model.StockAdjustment{ProductID: productID, Reason: f.Reason, Quantity: qty, Notes: f.Notes}
	if f.BatchID != "" {
		id, err := strconv.ParseInt(f.BatchID, 10, 64)
		if err != nil || id <= 0 {
			return nil, model.ErrInvalidAdjustmentBatch
		}
		a.BatchID = &id
	}
	return a, nil
}

// AdjustmentsProduct handles GET /adjustments/product?product_id=...: lo que
// la salida necesita saber del producto elegido: sus lotes (para elegir de
// cuál sale) y si ya está contado en el conteo abierto.
func (h *WebHandler) AdjustmentsProduct(w http.ResponseWriter, r *http.Request) {
	productID, _ := strconv.ParseInt(r.FormValue("product_id"), 10, 64)
	data, err := h.adjustmentProductData(r, productID, "")
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	h.renderPartial(w, r, "adjustment_product.gohtml", data)
}

// adjustmentProductData arma adjustment_product.gohtml. Sin producto, vacío.
func (h *WebHandler) adjustmentProductData(r *http.Request, productID int64, batchID string) (map[string]any, error) {
	data := map[string]any{"BatchID": batchID}
	if productID <= 0 {
		return data, nil
	}
	batches, err := h.adjustments.Batches(r.Context(), productID)
	if err != nil {
		return nil, err
	}
	counted, err := h.adjustments.InOpenCount(r.Context(), productID)
	if err != nil {
		return nil, err
	}
	data["Batches"], data["InOpenCount"] = batches, counted
	return data, nil
}

// adjustmentFormData arma los datos de adjustment_form.gohtml.
func (h *WebHandler) adjustmentFormData(r *http.Request, form adjustmentForm, last *adjustmentLast,
	errMsg string) (map[string]any, error) {
	productID, _ := strconv.ParseInt(form.ProductID, 10, 64)
	product, err := h.adjustmentProductData(r, productID, form.BatchID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"Form": form, "Reasons": model.ExitReasons, "Product": product,
		"Last": last, "Error": errMsg,
	}, nil
}
