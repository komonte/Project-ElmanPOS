package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/combos"
)

// Componentes de un combo (F-05), en el panel de edición del combo. Como los
// códigos de barra, cada acción guarda al momento y devuelve la sección
// entera (product_components.gohtml). Solo el admin cambia la receta
// (permiso combos, en la ruta); quien edita productos la ve sin el
// formulario.

// WithCombos le da al handler la receta de los combos.
func (h *WebHandler) WithCombos(c *combos.Service) *WebHandler {
	h.combos = c
	return h
}

// renderProductComponents redibuja la sección de componentes, con un error
// (y la cantidad escrita) si la acción falló.
func (h *WebHandler) renderProductComponents(w http.ResponseWriter, r *http.Request, packID int64, errMsg, qty string) {
	comps, err := h.combos.List(r.Context(), packID)
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	h.renderPartial(w, r, "product_components.gohtml", map[string]any{
		"ProductID": packID, "Components": comps, "Can": requestCan(r),
		"ComponentError": errMsg, "QuantityValue": qty,
	})
}

// ProductComponentsAdd handles POST /products/{id}/components: component_id
// (el botón del producto elegido en la búsqueda) y quantity. Si el
// componente ya estaba, le cambia la cantidad.
func (h *WebHandler) ProductComponentsAdd(w http.ResponseWriter, r *http.Request) {
	packID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	qtyText := r.FormValue("quantity")
	compID, _ := strconv.ParseInt(r.FormValue("component_id"), 10, 64) // vacío o mal: 0, "elegí el producto"
	qty, ok := parseFormDecimal(qtyText)
	if !ok || strings.TrimSpace(qtyText) == "" {
		h.renderProductComponents(w, r, packID, h.tr(r, "error.component_quantity"), qtyText)
		return
	}
	if _, err := h.combos.Set(r.Context(), packID, compID, qty); err != nil {
		// Un combo que no existe (lo borraron con el panel abierto) no tiene
		// sección que redibujar.
		if errors.Is(err, service.ErrNotFound) || errors.Is(err, service.ErrInvalidID) {
			h.actionError(w, r, err)
			return
		}
		h.renderProductComponents(w, r, packID, h.errMsg(r, err), qtyText)
		return
	}
	h.renderProductComponents(w, r, packID, "", "")
}

// ProductComponentsRemove handles DELETE /products/{id}/components/{componentID}
// (componentID es el renglón de la receta).
func (h *WebHandler) ProductComponentsRemove(w http.ResponseWriter, r *http.Request) {
	packID, err1 := strconv.ParseInt(r.PathValue("id"), 10, 64)
	rowID, err2 := strconv.ParseInt(r.PathValue("componentID"), 10, 64)
	if err1 != nil || err2 != nil {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	if err := h.combos.Remove(r.Context(), packID, rowID); err != nil {
		h.actionError(w, r, err)
		return
	}
	h.renderProductComponents(w, r, packID, "", "")
}

// ProductComponentsSearch handles GET /products/{id}/components/search?q=...:
// los productos que pueden ir en el combo, como botones del formulario.
func (h *WebHandler) ProductComponentsSearch(w http.ResponseWriter, r *http.Request) {
	packID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	q := strings.TrimSpace(r.FormValue("q"))
	data := map[string]any{"Query": q}
	if q != "" {
		found, err := h.combos.Candidates(r.Context(), packID, q)
		if err != nil {
			h.actionError(w, r, err)
			return
		}
		data["Options"] = found
	}
	h.renderPartial(w, r, "product_component_options.gohtml", data)
}
