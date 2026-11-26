package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/i18n"
	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sessions"
)

// Caja (F-11, F-12): crear "Caja 1" si el local no tiene, abrirla con el
// efectivo inicial, ver si está abierta y cerrarla con el arqueo.
//
// El cierre es ciego: el formulario no muestra lo esperado. Para que sea
// fácil contar, cada medio dice dónde mirar (el cajón, el cierre de lote del
// posnet, el home banking, la app del QR) y cuántos cobros hubo con él, sin
// el monto. Después de cerrar, /cash/sessions/{id} muestra si sobra o falta.

// CashPage handles GET /cash. Con ?voided={id} (después de anular) muestra
// qué devolverle al cliente.
func (h *WebHandler) CashPage(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{}
	if id, err := strconv.ParseInt(r.FormValue("voided"), 10, 64); err == nil && id > 0 {
		data["VoidedID"] = id
	}
	h.renderCash(w, r, data)
}

// CashCreateRegister handles POST /cash/register: crea "Caja 1" (o devuelve
// la que ya hay) y vuelve a la página.
func (h *WebHandler) CashCreateRegister(w http.ResponseWriter, r *http.Request) {
	if _, err := h.sessions.CreateRegister(r.Context()); err != nil {
		h.renderCash(w, r, map[string]any{"Error": h.errMsg(r, err)})
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
		h.renderCash(w, r, map[string]any{"Error": h.errMsg(r, model.ErrInvalidOpeningAmount), "Amount": typed})
		return
	}
	if _, err := h.sessions.Open(r.Context(), amount); err != nil {
		h.renderCash(w, r, map[string]any{"Error": h.errMsg(r, err), "Amount": typed})
		return
	}
	http.Redirect(w, r, "/cash", http.StatusSeeOther)
}

// CashClose handles POST /cash/close: cierra la sesión session_id con lo
// declarado por medio (declared_CASH, declared_DEBIT_CARD...). Vacío es 0.
// Bien: va al resumen del cierre. Con error, la página vuelve con lo escrito,
// el formulario abierto y el medio con problemas marcado.
func (h *WebHandler) CashClose(w http.ResponseWriter, r *http.Request) {
	sessionID, _ := strconv.ParseInt(r.FormValue("session_id"), 10, 64)
	declared, typed, badMethod := declaredFromForm(r)
	if badMethod != "" {
		lang := transport.GetLocale(r.Context())
		msg := i18n.T(lang, "pos.method_"+badMethod) + ": " + i18n.T(lang, "error.cash_declared_invalid")
		h.renderCash(w, r, map[string]any{"CloseError": msg, "Typed": typed, "Invalid": badMethod})
		return
	}
	sum, err := h.sessions.Close(r.Context(), sessionID, declared)
	if err != nil {
		h.renderCash(w, r, map[string]any{"CloseError": h.errMsg(r, err), "Typed": typed})
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/cash/sessions/%d", sum.Session.ID), http.StatusSeeOther)
}

// declaredFromForm lee lo declarado al cerrar: declared_<MEDIO> por medio,
// escrito como plata ("15.000"). Un medio vacío no va (cuenta como 0).
// typed es lo escrito, para volver a mostrarlo. badMethod es el primer
// medio mal escrito (o negativo, o con más de 2 decimales), o vacío.
func declaredFromForm(r *http.Request) (declared []*model.CashAudit, typed map[string]string, badMethod string) {
	typed = make(map[string]string, len(model.PaymentMethods))
	for _, m := range model.PaymentMethods {
		v := strings.TrimSpace(r.FormValue("declared_" + m))
		typed[m] = v
		if v == "" || badMethod != "" {
			continue
		}
		amount, ok := parseFormMoney(v)
		a := &model.CashAudit{Method: m, DeclaredAmount: amount}
		if !ok || a.Validate() != nil {
			badMethod = m
			continue
		}
		declared = append(declared, a)
	}
	return declared, typed, badMethod
}

// CashVoidSale handles POST /cash/sales/{id}/void: anula la venta con el
// motivo (F-08). Bien: vuelve a /cash?voided={id}, que dice qué devolver (y
// recargar no la anula de nuevo). Con error, la página vuelve con la fila
// abierta, lo escrito y el mensaje.
func (h *WebHandler) CashVoidSale(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	reason := r.FormValue("reason")
	if _, err := h.sales.Void(r.Context(), id, reason); err != nil {
		h.renderCash(w, r, map[string]any{"VoidError": h.errMsg(r, err), "VoidSaleID": id, "VoidReason": reason})
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/cash?voided=%d", id), http.StatusSeeOther)
}

// refundNotice dice qué devolverle al cliente por una venta anulada, por
// medio y en el orden de PaymentMethods: "Venta N.º 12 anulada. Devolvé
// $ 2.000,00 en efectivo y $ 3.000,00 por débito." El efectivo ya viene sin
// el vuelto (SettlePayments).
func refundNotice(lang string, sale *model.Sale) string {
	paid := make(map[string]decimal.Decimal, len(sale.Payments))
	for _, p := range sale.Payments {
		paid[p.Method] = paid[p.Method].Add(p.Amount)
	}
	var parts []string
	for _, m := range model.PaymentMethods {
		if amount, ok := paid[m]; ok {
			parts = append(parts, fmt.Sprintf(i18n.T(lang, "cash.refund_"+m), formatMoney(lang, amount)))
		}
	}
	list := ""
	if n := len(parts); n > 0 {
		list = parts[n-1]
		if n > 1 {
			list = strings.Join(parts[:n-1], ", ") + i18n.T(lang, "cash.refund_and") + list
		}
	}
	return fmt.Sprintf(i18n.T(lang, "cash.voided_notice"), sale.SaleNumber, list)
}

// CashSession handles GET /cash/sessions/{id}: el resumen de un cierre. Una
// sesión abierta no tiene resumen (cierre ciego): vuelve a /cash.
func (h *WebHandler) CashSession(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	sum, err := h.sessions.Summary(r.Context(), id)
	switch {
	case errors.Is(err, sessions.ErrSessionStillOpen):
		http.Redirect(w, r, "/cash", http.StatusSeeOther)
		return
	case errors.Is(err, service.ErrNotFound), errors.Is(err, service.ErrInvalidID):
		http.Error(w, h.errMsg(r, err), http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, h.errMsg(r, err), http.StatusInternalServerError)
		return
	}
	closedAt := ""
	if sum.Session.ClosedAt != nil {
		closedAt = *sum.Session.ClosedAt
	}
	h.render(w, r, "cash_session.gohtml", map[string]any{
		"Section": "cash", "TitleKey": "cash.summary_title", "Summary": sum, "ClosedAt": closedAt,
		"Rows": closeRows(sum.Close), "HasDifference": sum.Close.HasDifference(),
	})
}

// closeRow es una fila del resumen del cierre.
type closeRow struct {
	Method   string
	Expected decimal.Decimal
	Declared decimal.Decimal
	// Diff es la diferencia sin signo; Short dice si falta (si no, sobra o
	// cierra justo: Diff en 0).
	Diff  decimal.Decimal
	Short bool
}

// closeRows son las filas del resumen: el efectivo siempre y los otros
// medios solo si tuvieron cobros o se declaró algo (un medio sin nada no
// aporta y alarga la tabla).
func closeRows(s *model.CloseSummary) []closeRow {
	var rows []closeRow
	for _, l := range s.Lines {
		if l.Method != model.PaymentCash && l.Expected.IsZero() && l.Declared.IsZero() {
			continue
		}
		d := l.Difference()
		rows = append(rows, closeRow{Method: l.Method, Expected: l.Expected, Declared: l.Declared,
			Diff: d.Abs(), Short: d.IsNegative()})
	}
	return rows
}

// closeField es un medio del formulario de cierre.
type closeField struct {
	Method  string
	Typed   string // lo escrito, si el cierre falló
	Count   int    // cobros de la sesión con este medio
	Invalid bool
}

// renderCash dibuja la página con el estado de la caja. data trae lo que
// vuelve de un formulario que falló (Error, Amount; CloseError, Typed,
// Invalid; VoidError, VoidSaleID, VoidReason) y, después de anular,
// VoidedID. Con la caja abierta suma los campos del cierre (CloseFields),
// las ventas de la sesión (Sales) y qué devolver por la anulada (Refund). Un error al leer el estado (sin local activo, dos cajas) se
// muestra en lugar de los formularios.
func (h *WebHandler) renderCash(w http.ResponseWriter, r *http.Request, data map[string]any) {
	if data == nil {
		data = make(map[string]any)
	}
	data["Section"], data["TitleKey"] = "cash", "cash.title"
	st, err := h.sessions.Current(r.Context())
	if err != nil {
		data["Error"] = h.errMsg(r, err)
		h.render(w, r, "cash.gohtml", data)
		return
	}
	data["State"] = st
	if st.Session != nil {
		counts, err := h.sessions.PaymentCounts(r.Context(), st.Session.ID)
		if err != nil {
			data["Error"] = h.errMsg(r, err)
		}
		typed, _ := data["Typed"].(map[string]string)
		invalid, _ := data["Invalid"].(string)
		fields := make([]closeField, 0, len(model.PaymentMethods))
		for _, m := range model.PaymentMethods {
			fields = append(fields, closeField{Method: m, Typed: typed[m], Count: counts[m], Invalid: m == invalid})
		}
		data["CloseFields"] = fields
		list, err := h.sessions.Sales(r.Context(), st.Session.ID)
		if err != nil {
			data["Error"] = h.errMsg(r, err)
		}
		data["Sales"] = list
		h.voidResult(r, data, list)
	}
	h.render(w, r, "cash.gohtml", data)
}

// voidResult completa lo de la anulación con las ventas de la sesión:
//   - VoidedID: si es una anulada de esta sesión, Refund dice qué devolver.
//     Si no (una URL vieja o de otra caja), no se avisa nada.
//   - VoidError: va en la fila de la venta; si la venta no está en la lista
//     (no existe o es de otro día), arriba de todo.
func (h *WebHandler) voidResult(r *http.Request, data map[string]any, list []*model.Sale) {
	find := func(id int64) *model.Sale {
		for _, s := range list {
			if s.ID == id {
				return s
			}
		}
		return nil
	}
	if id, ok := data["VoidedID"].(int64); ok {
		if s := find(id); s != nil && s.Status == model.SaleVoided {
			data["Refund"] = refundNotice(transport.GetLocale(r.Context()), s)
		}
	}
	if msg, ok := data["VoidError"].(string); ok {
		id, _ := data["VoidSaleID"].(int64)
		if s := find(id); s == nil || s.Status != model.SaleCompleted {
			data["Error"], data["VoidError"] = msg, ""
		}
	}
}
