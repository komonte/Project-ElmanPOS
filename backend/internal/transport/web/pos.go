package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/i18n"
	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/sales"
)

// Vender (F-07, F-10). El carrito vive en la página (JavaScript en
// pos.gohtml) hasta cobrar: el servidor solo busca productos y cobra. Al
// cobrar, la caja manda productos, cantidades y pagos; precios, IVA y stock
// los pone sales.Checkout.
//
// No usa el buscador compartido de compras y conteos (picker.go): en la caja
// no se da de alta un producto ni se le agrega un código en medio de una
// venta, y cada resultado necesita su precio y su stock.

// PosPage handles GET /pos.
func (h *WebHandler) PosPage(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Section": "pos", "TitleKey": "pos.title", "Methods": model.PaymentMethods}
	st, err := h.sessions.Current(r.Context())
	if err != nil {
		data["Error"] = h.errMsg(r, err)
	} else {
		data["State"] = st
	}
	h.render(w, r, "pos.gohtml", data)
}

// PosSearch handles GET /pos/search?q=...: los resultados del buscador de la
// caja, con precio y stock.
func (h *WebHandler) PosSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.FormValue("q"))
	data := map[string]any{"Query": q}
	if q != "" {
		options, err := h.sales.Search(r.Context(), q)
		if err != nil {
			h.actionError(w, r, err)
			return
		}
		data["Options"] = options
	}
	h.renderPartial(w, r, "pos_options.gohtml", data)
}

// PosCheckout handles POST /pos/checkout: cobra el carrito. Responde
// pos_result.gohtml: con la venta (y la página vacía el carrito) o con el
// error (y el carrito queda como estaba, para corregir y volver a cobrar).
func (h *WebHandler) PosCheckout(w http.ResponseWriter, r *http.Request) {
	in, err := saleFromForm(r)
	if err == nil {
		var sale *model.Sale
		if sale, err = h.sales.Checkout(r.Context(), in); err == nil {
			h.renderPartial(w, r, "pos_result.gohtml", map[string]any{"Sale": sale})
			return
		}
	}
	h.renderPartial(w, r, "pos_result.gohtml", map[string]any{"Error": h.posErrMsg(r, err)})
}

// saleFromForm lee el carrito: product_id y quantity por renglón, y
// pay_method, pay_amount y pay_ref por pago, en el mismo orden. Los importes
// se leen como se escribe la plata (parseFormMoney: "10.000").
func saleFromForm(r *http.Request) (*model.Sale, error) {
	if err := r.ParseForm(); err != nil {
		return nil, err
	}
	ids, qtys := r.PostForm["product_id"], r.PostForm["quantity"]
	if len(ids) != len(qtys) {
		return nil, model.ErrInvalidSaleQuantity
	}
	sale := &model.Sale{}
	for i := range ids {
		id, err := strconv.ParseInt(ids[i], 10, 64)
		if err != nil || id <= 0 {
			return nil, model.ErrSaleItemProduct
		}
		qty, ok := parseFormDecimal(qtys[i])
		if !ok || strings.TrimSpace(qtys[i]) == "" {
			return nil, model.ErrInvalidSaleQuantity
		}
		sale.Items = append(sale.Items, &model.SaleItem{ProductID: id, Quantity: qty})
	}

	methods, amounts, refs := r.PostForm["pay_method"], r.PostForm["pay_amount"], r.PostForm["pay_ref"]
	if len(methods) != len(amounts) || len(methods) != len(refs) {
		return nil, model.ErrInvalidPaymentAmount
	}
	for i := range methods {
		amount, ok := parseFormMoney(amounts[i])
		if !ok {
			return nil, model.ErrInvalidPaymentAmount
		}
		sale.Payments = append(sale.Payments, &model.Payment{Method: methods[i], Amount: amount, Reference: refs[i]})
	}
	return sale, nil
}

// posErrMsg es el mensaje de un error al cobrar. Un problema de un renglón
// lleva el nombre del producto adelante ("SODA: ...") y, si falta stock,
// cuánto hay.
func (h *WebHandler) posErrMsg(r *http.Request, err error) string {
	return h.posErrText(r, transport.GetLocale(r.Context()), err)
}

func (h *WebHandler) posErrText(r *http.Request, lang string, err error) string {
	var line *sales.LineError
	if !errors.As(err, &line) {
		return h.errText(r, lang, err)
	}
	msg := h.errText(r, lang, line.Err)
	if errors.Is(line.Err, sales.ErrNotEnoughStock) {
		msg = fmt.Sprintf(i18n.T(lang, "error.sale_not_enough"), formatQuantity(lang, line.Available))
	}
	if line.ProductName == "" {
		return msg
	}
	return line.ProductName + ": " + msg
}
