package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/transport"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/customers"
)

// Clientes para fiar (migración 0025): la página es del admin (permiso
// customers). La caja da de alta con solo el nombre al fiar (PR9).

// WithCustomers le da al handler los clientes.
func (h *WebHandler) WithCustomers(c *customers.Service) *WebHandler {
	h.customers = c
	return h
}

// CustomersList handles GET /customers[?search=].
func (h *WebHandler) CustomersList(w http.ResponseWriter, r *http.Request) {
	// La búsqueda (htmx) redibuja solo la tabla.
	if r.Header.Get("HX-Request") != "" {
		h.customersTable(w, r)
		return
	}
	q := strings.TrimSpace(r.FormValue("search"))
	list, err := h.customers.List(r.Context(), q)
	if err != nil {
		http.Error(w, h.errMsg(r, err), http.StatusInternalServerError)
		return
	}
	h.render(w, r, "customers.gohtml", map[string]any{
		"Customers": list, "Search": q, "Section": "customers", "TitleKey": "customer.title",
	})
}

func (h *WebHandler) customersTable(w http.ResponseWriter, r *http.Request) {
	list, err := h.customers.List(r.Context(), strings.TrimSpace(r.FormValue("search")))
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	h.renderPartial(w, r, "customer_table.gohtml", map[string]any{"Customers": list})
}

// renderCustomerForm dibuja el alta o la edición. c es nil en un alta vacía.
func (h *WebHandler) renderCustomerForm(w http.ResponseWriter, r *http.Request, c *model.Customer, errMsg string) {
	h.renderPartial(w, r, "customer_form.gohtml", map[string]any{"Customer": c, "Error": errMsg})
}

func customerFromForm(r *http.Request) *model.Customer {
	return &model.Customer{Name: r.FormValue("name"), Phone: r.FormValue("phone"), Notes: r.FormValue("notes")}
}

// CustomersNewForm handles GET /customers/new.
func (h *WebHandler) CustomersNewForm(w http.ResponseWriter, r *http.Request) {
	h.renderCustomerForm(w, r, nil, "")
}

// CustomersCreate handles POST /customers/new.
func (h *WebHandler) CustomersCreate(w http.ResponseWriter, r *http.Request) {
	c := customerFromForm(r)
	if _, err := h.customers.Create(r.Context(), transport.UserID(r.Context()), c); err != nil {
		h.renderCustomerForm(w, r, c, h.errMsg(r, err))
		return
	}
	w.Header().Set("HX-Redirect", "/customers")
	w.WriteHeader(http.StatusOK)
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// CustomersEditForm handles GET /customers/{id}/edit.
func (h *WebHandler) CustomersEditForm(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	c, err := h.customers.Get(r.Context(), id)
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	h.renderCustomerForm(w, r, c, "")
}

// CustomersUpdate handles POST /customers/{id}/edit.
func (h *WebHandler) CustomersUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	c := customerFromForm(r)
	c.ID = id
	if _, err := h.customers.Update(r.Context(), transport.UserID(r.Context()), id, c); err != nil {
		h.renderCustomerForm(w, r, c, h.errMsg(r, err))
		return
	}
	w.Header().Set("HX-Redirect", "/customers")
	w.WriteHeader(http.StatusOK)
}

func (h *WebHandler) customerSetActive(w http.ResponseWriter, r *http.Request, active bool) {
	id, ok := pathID(r)
	if !ok {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	if err := h.customers.SetActive(r.Context(), transport.UserID(r.Context()), id, active); err != nil {
		h.actionError(w, r, err)
		return
	}
	h.customersTable(w, r)
}

// CustomersDeactivate handles PATCH /customers/{id}/deactivate.
func (h *WebHandler) CustomersDeactivate(w http.ResponseWriter, r *http.Request) {
	h.customerSetActive(w, r, false)
}

// CustomersReactivate handles PATCH /customers/{id}/reactivate.
func (h *WebHandler) CustomersReactivate(w http.ResponseWriter, r *http.Request) {
	h.customerSetActive(w, r, true)
}

// Fiado en la caja (permiso sell): buscar el cliente, con lo que debe, o
// darlo de alta con solo el nombre.

// PosCustomers handles GET /pos/customers?q=...: los clientes activos que
// coinciden, para elegir uno al cobrar con cuenta corriente.
func (h *WebHandler) PosCustomers(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.FormValue("q"))
	data := map[string]any{"Query": q}
	if q != "" {
		found, err := h.customers.SearchWithDebt(r.Context(), q)
		if err != nil {
			h.actionError(w, r, err)
			return
		}
		data["Customers"] = found
		exact := false
		for _, c := range found {
			if strings.EqualFold(strings.Join(strings.Fields(c.Name), " "), strings.Join(strings.Fields(q), " ")) {
				exact = true
			}
		}
		data["OfferNew"] = !exact
	}
	h.renderPartial(w, r, "pos_customers.gohtml", data)
}

// PosCustomersCreate handles POST /pos/customers (name): da de alta un
// cliente con solo el nombre y lo devuelve ya elegido (data-created). Un
// error (nombre repetido, muy corto) va como un renglón de la lista.
func (h *WebHandler) PosCustomersCreate(w http.ResponseWriter, r *http.Request) {
	c, err := h.customers.Create(r.Context(), transport.UserID(r.Context()), &model.Customer{Name: r.FormValue("name")})
	if err != nil {
		h.renderPartial(w, r, "pos_customers.gohtml", map[string]any{"Error": h.errMsg(r, err)})
		return
	}
	h.renderPartial(w, r, "pos_customers.gohtml", map[string]any{
		"Customers": []customers.WithDebt{{Customer: c}}, "Created": true,
	})
}

// Cobrar una deuda en la caja (0027, permiso sell).

// PosCollectPage handles GET /pos/collect[?customer=ID]: sin cliente, el
// buscador; con cliente, cuánto debe y el formulario.
func (h *WebHandler) PosCollectPage(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Section": "pos", "TitleKey": "collect.title", "Methods": model.PaymentMethods,
		"Method": model.PaymentCash}
	if v := r.FormValue("customer"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || !h.collectCustomer(r, data, id) {
			data["Error"] = h.tr(r, "error.not_found")
		}
	}
	h.render(w, r, "pos_collect.gohtml", data)
}

// collectCustomer pone en data el cliente id, su deuda y el monto
// precargado (la deuda). false si no existe.
func (h *WebHandler) collectCustomer(r *http.Request, data map[string]any, id int64) bool {
	c, err := h.customers.Get(r.Context(), id)
	if err != nil {
		return false
	}
	debt, err := h.customers.Debt(r.Context(), id)
	if err != nil {
		return false
	}
	data["Customer"], data["Debt"] = c, debt
	if _, ok := data["Amount"]; !ok && debt.IsPositive() {
		data["Amount"] = formatMoneyInput(transport.GetLocale(r.Context()), debt)
	}
	return true
}

// PosCollectCustomers handles GET /pos/collect/customers?q=...: los
// clientes que coinciden, como links a su cobro.
func (h *WebHandler) PosCollectCustomers(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.FormValue("q"))
	data := map[string]any{"Query": q, "Collect": true}
	if q != "" {
		found, err := h.customers.SearchWithDebt(r.Context(), q)
		if err != nil {
			h.actionError(w, r, err)
			return
		}
		data["Customers"] = found
	}
	h.renderPartial(w, r, "pos_customers.gohtml", data)
}

// PosCollect handles POST /pos/collect: customer_id, method, amount (lo
// entregado) y reference. Vuelve a la página con el resultado o el error.
func (h *WebHandler) PosCollect(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{"Section": "pos", "TitleKey": "collect.title", "Methods": model.PaymentMethods,
		"Method": r.FormValue("method"), "Amount": r.FormValue("amount"), "Reference": r.FormValue("reference")}
	id, err := strconv.ParseInt(r.FormValue("customer_id"), 10, 64)
	if err != nil || id <= 0 {
		data["Error"] = h.tr(r, "error.invalid_id")
		h.render(w, r, "pos_collect.gohtml", data)
		return
	}
	amount, ok := parseFormMoney(r.FormValue("amount"))
	if !ok {
		data["Error"] = h.errMsg(r, model.ErrCustomerPaymentAmount)
		h.collectCustomer(r, data, id)
		h.render(w, r, "pos_collect.gohtml", data)
		return
	}
	done, err := h.customers.Collect(r.Context(), transport.UserID(r.Context()), id, r.FormValue("method"), amount,
		r.FormValue("reference"))
	if err != nil {
		data["Error"] = h.errMsg(r, err)
		h.collectCustomer(r, data, id)
		h.render(w, r, "pos_collect.gohtml", data)
		return
	}
	// Cobrado: el resultado y el buscador para el próximo.
	h.render(w, r, "pos_collect.gohtml", map[string]any{"Section": "pos", "TitleKey": "collect.title", "Done": done})
}

// Deudores y cuenta de un cliente (solo el admin, permiso customers).

// ReportsDebtors handles GET /reports/debtors.
func (h *WebHandler) ReportsDebtors(w http.ResponseWriter, r *http.Request) {
	list, err := h.customers.Debtors(r.Context())
	if err != nil {
		http.Error(w, h.errMsg(r, err), http.StatusInternalServerError)
		return
	}
	total := decimal.Zero
	for _, d := range list {
		total = total.Add(d.Debt)
	}
	h.render(w, r, "report_debtors.gohtml", map[string]any{"Section": "reports_debtors",
		"TitleKey": "debtors.title", "Debtors": list, "Total": total})
}

// CustomerAccount handles GET /customers/{id}/account: lo fiado y lo pagado,
// con el saldo.
func (h *WebHandler) CustomerAccount(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		http.Error(w, h.tr(r, "error.invalid_id"), http.StatusBadRequest)
		return
	}
	c, entries, err := h.customers.Statement(r.Context(), id)
	if err != nil {
		h.actionError(w, r, err)
		return
	}
	debt := decimal.Zero
	if n := len(entries); n > 0 {
		debt = entries[n-1].Balance
	}
	h.render(w, r, "customer_account.gohtml", map[string]any{"Section": "reports_debtors",
		"TitleKey": "debtors.account_title", "Customer": c, "Entries": entries, "Debt": debt})
}
