package web

import (
	"net/http"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
)

// Caja (F-11): crear "Caja 1" si el local no tiene, abrirla con el efectivo
// inicial y ver si está abierta. El cierre con arqueo (F-12) llega después.

// CashPage handles GET /cash.
func (h *WebHandler) CashPage(w http.ResponseWriter, r *http.Request) {
	h.renderCash(w, r, "", "")
}

// CashCreateRegister handles POST /cash/register: crea "Caja 1" (o devuelve
// la que ya hay) y vuelve a la página.
func (h *WebHandler) CashCreateRegister(w http.ResponseWriter, r *http.Request) {
	if _, err := h.sessions.CreateRegister(r.Context()); err != nil {
		h.renderCash(w, r, h.errMsg(r, err), "")
		return
	}
	http.Redirect(w, r, "/cash", http.StatusSeeOther)
}

// CashOpen handles POST /cash/open: abre la caja con el efectivo inicial.
// Con error, la página vuelve con lo escrito y el mensaje.
func (h *WebHandler) CashOpen(w http.ResponseWriter, r *http.Request) {
	typed := strings.TrimSpace(r.FormValue("opening_amount"))
	amount, ok := parseFormMoney(typed)
	if !ok {
		h.renderCash(w, r, h.errMsg(r, model.ErrInvalidOpeningAmount), typed)
		return
	}
	if _, err := h.sessions.Open(r.Context(), amount); err != nil {
		h.renderCash(w, r, h.errMsg(r, err), typed)
		return
	}
	http.Redirect(w, r, "/cash", http.StatusSeeOther)
}

// renderCash dibuja la página con el estado de la caja. Un error al leer el
// estado (sin local activo, dos cajas) se muestra en lugar del formulario.
func (h *WebHandler) renderCash(w http.ResponseWriter, r *http.Request, errMsg, typed string) {
	data := map[string]any{"Section": "cash", "TitleKey": "cash.title", "Error": errMsg, "Amount": typed}
	st, err := h.sessions.Current(r.Context())
	if err != nil {
		data["Error"] = h.errMsg(r, err)
	} else {
		data["State"] = st
	}
	h.render(w, r, "cash.gohtml", data)
}
