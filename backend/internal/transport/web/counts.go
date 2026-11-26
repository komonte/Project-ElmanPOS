package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/store"
)

// Conteo de stock (F-09), que también es el inventario inicial. Se cuenta en
// un borrador, por sectores: se elige el sector, se escanea (el buscador
// compartido, picker.go) y se escribe la cantidad. Mientras se cuenta no se
// muestra lo que dice el sistema, para que no influya en lo que se cuenta: la
// comparación se ve al revisar antes de confirmar.

// countEntryForm es lo que se escribió en la carga, para volver a dibujarlo
// si algo falla. El sector se conserva también después de guardar: se
// cuenta un sector entero antes de pasar al siguiente.
type countEntryForm struct {
	Sector, ProductID, ProductQ, Quantity string
}

// countLast es lo último que se cargó, para confirmarlo en pantalla ("Se
// cargó: SODA COLA × 12 en Góndola 3").
type countLast struct {
	Name, Sector string
	Quantity     decimal.Decimal
	Total        decimal.Decimal // lo contado del producto, sumando sectores
}

// CountsPage handles GET /counts: el borrador abierto o, si no hay, el botón
// para empezar uno. Mirar la página no abre un conteo.
func (h *WebHandler) CountsPage(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Section": "counts", "TitleKey": "count.title"}
	c, err := h.stockCounts.Current(r.Context())
	switch {
	case errors.Is(err, service.ErrNotFound):
	case err != nil:
		data["Error"] = h.errMsg(r, err)
	default:
		sheet, err := h.countSheetData(r, c, countEntryForm{}, nil, "")
		if err != nil {
			http.Error(w, h.errMsg(r, err), http.StatusInternalServerError)
			return
		}
		data["Sheet"] = sheet
	}
	h.render(w, r, "counts.gohtml", data)
}

// CountsOpen handles POST /counts: abre el borrador (o sigue el abierto) y
// vuelve a la página.
func (h *WebHandler) CountsOpen(w http.ResponseWriter, r *http.Request) {
	if _, err := h.stockCounts.Open(r.Context()); err != nil {
		h.render(w, r, "counts.gohtml", map[string]any{"Section": "counts", "TitleKey": "count.title",
			"Error": h.errMsg(r, err)})
		return
	}
	http.Redirect(w, r, "/counts", http.StatusSeeOther)
}

// CountsAddEntry handles POST /counts/{id}/entries: guarda una carga y
// responde la planilla, con el sector puesto y el cursor en el buscador para
// el producto siguiente. Con error, la planilla vuelve con lo escrito.
func (h *WebHandler) CountsAddEntry(w http.ResponseWriter, r *http.Request) {
	countID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	form := countEntryForm{
		Sector:    strings.TrimSpace(r.FormValue("sector")),
		ProductID: strings.TrimSpace(r.FormValue("product_id")),
		ProductQ:  strings.TrimSpace(r.FormValue("product_q")),
		Quantity:  strings.TrimSpace(r.FormValue("quantity")),
	}

	var last *countLast
	entry, err := countEntryFromForm(form)
	if err == nil {
		var item *model.StockCountItem
		if item, err = h.stockCounts.AddEntry(r.Context(), countID, entry); err == nil {
			last = &countLast{Name: form.ProductQ, Sector: entry.Sector, Quantity: entry.Quantity,
				Total: item.CountedQuantity}
			form = countEntryForm{Sector: entry.Sector} // listo para el producto siguiente
		}
	}
	errMsg := ""
	if err != nil {
		errMsg = h.errMsg(r, err)
	}
	h.renderCountSheet(w, r, countID, form, last, errMsg)
}

// countEntryFromForm convierte la carga del formulario. Sin producto elegido
// o con una cantidad vacía o mal escrita, el error del modelo (como Validate).
func countEntryFromForm(f countEntryForm) (*model.StockCountEntry, error) {
	productID, _ := strconv.ParseInt(f.ProductID, 10, 64)
	if productID <= 0 {
		return nil, model.ErrStockCountEntryProduct
	}
	qty, ok := parseFormDecimal(f.Quantity)
	if !ok || f.Quantity == "" {
		return nil, model.ErrInvalidCountedQuantity
	}
	return &model.StockCountEntry{ProductID: productID, Sector: f.Sector, Quantity: qty}, nil
}

// CountsRemoveEntry handles POST /counts/{id}/entries/{entryID}/delete: quita
// una carga (el × de la planilla) y responde la planilla.
func (h *WebHandler) CountsRemoveEntry(w http.ResponseWriter, r *http.Request) {
	countID, err1 := strconv.ParseInt(r.PathValue("id"), 10, 64)
	entryID, err2 := strconv.ParseInt(r.PathValue("entryID"), 10, 64)
	if err1 != nil || err2 != nil {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	errMsg := ""
	if err := h.stockCounts.RemoveEntry(r.Context(), countID, entryID); err != nil {
		errMsg = h.errMsg(r, err)
	}
	form := countEntryForm{Sector: strings.TrimSpace(r.FormValue("sector"))}
	h.renderCountSheet(w, r, countID, form, nil, errMsg)
}

// CountsDiscard handles POST /counts/{id}/discard: descarta el borrador con
// todo lo cargado (la página pide confirmación antes) y vuelve a la página.
func (h *WebHandler) CountsDiscard(w http.ResponseWriter, r *http.Request) {
	countID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	if err := h.stockCounts.Discard(r.Context(), countID); err != nil &&
		!errors.Is(err, service.ErrNotFound) { // ya no estaba: la página queda igual
		h.render(w, r, "counts.gohtml", map[string]any{"Section": "counts", "TitleKey": "count.title",
			"Error": h.errMsg(r, err)})
		return
	}
	http.Redirect(w, r, "/counts", http.StatusSeeOther)
}

// renderCountSheet responde la planilla del conteo. Si el conteo ya no está
// en borrador (otro lo confirmó o descartó) avisa y manda a recargar.
func (h *WebHandler) renderCountSheet(w http.ResponseWriter, r *http.Request, countID int64,
	form countEntryForm, last *countLast, errMsg string) {
	c, err := h.stockCounts.Get(r.Context(), countID)
	if err == nil && c.Status != model.CountDraft {
		err = fmt.Errorf("%w: %w", service.ErrConflict, store.ErrCountNotDraft)
	}
	if err != nil {
		w.Header().Set("HX-Redirect", "/counts")
		h.actionError(w, r, err)
		return
	}
	data, err := h.countSheetData(r, c, form, last, errMsg)
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	h.renderPartial(w, r, "count_sheet.gohtml", data)
}

// countSheetData arma los datos de count_sheet.gohtml.
func (h *WebHandler) countSheetData(r *http.Request, c *model.StockCount, form countEntryForm,
	last *countLast, errMsg string) (map[string]any, error) {
	sectors, err := h.stockCounts.Sectors(r.Context())
	if err != nil {
		return nil, err
	}
	entries := 0
	for _, it := range c.Items {
		entries += len(it.Entries)
	}
	return map[string]any{
		"Count": c, "Entries": entries, "Sectors": sectors,
		"Form": form, "Last": last, "Error": errMsg,
	}, nil
}
