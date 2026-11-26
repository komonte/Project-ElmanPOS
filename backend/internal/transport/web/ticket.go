package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
)

// Ticket de una venta (no fiscal): una página sola, sin el menú, de 80 mm
// para la impresora térmica. La caja la imprime desde un iframe oculto
// (pos.gohtml) sin salir de Vender; la misma dirección sirve para reimprimir.
//
// El vuelto y "paga con" no se guardan. Para el ticket de la venta recién
// cobrada, pos_result.gohtml manda lo entregado en efectivo en la dirección
// (?paid=20000) y el vuelto se calcula contra el pago en efectivo guardado.
// Reimpreso, sin paid, el ticket no los muestra.

// PosTicket handles GET /pos/sales/{id}/ticket[?paid=...].
func (h *WebHandler) PosTicket(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	sale, err := h.sales.Get(r.Context(), id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, service.ErrNotFound) || errors.Is(err, service.ErrInvalidID) {
			status = http.StatusNotFound
		}
		http.Error(w, h.errMsg(r, err), status)
		return
	}
	data := map[string]any{"Sale": sale}
	if paid, change, ok := ticketCash(sale, r.URL.Query().Get("paid")); ok {
		data["Paid"], data["Change"] = paid, change
	}
	h.renderPartial(w, r, "ticket.gohtml", data)
}

// ticketCash lee lo entregado en efectivo (paid, como lo escribe
// ticketURL: "20000" o "20000.5") y da el vuelto contra el pago en efectivo
// de la venta. ok es false si no hay paid, si está mal escrito, si la venta
// no tiene efectivo o si paid no pasa ese pago (no hay vuelto que mostrar).
func ticketCash(sale *model.Sale, raw string) (paid, change decimal.Decimal, ok bool) {
	if raw == "" {
		return paid, change, false
	}
	paid, err := decimal.NewFromString(raw)
	if err != nil || paid.Exponent() < -2 {
		return paid, change, false
	}
	for _, p := range sale.Payments {
		if p.Method == model.PaymentCash && paid.GreaterThan(p.Amount) {
			return paid, paid.Sub(p.Amount), true
		}
	}
	return paid, change, false
}

// ticketURL es la dirección del ticket de una venta recién cobrada: con lo
// entregado en efectivo si hubo vuelto. Se escribe con punto decimal y sin
// miles (decimal.String), no como la plata a mano: el que la lee es
// ticketCash, no parseFormMoney.
func ticketURL(sale *model.Sale) string {
	u := fmt.Sprintf("/pos/sales/%d/ticket", sale.ID)
	if !sale.Change.IsPositive() {
		return u
	}
	for _, p := range sale.Payments {
		if p.Method == model.PaymentCash {
			return u + "?paid=" + p.Tendered.String()
		}
	}
	return u
}
