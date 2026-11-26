package transport

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/purchases"
)

// PurchaseService es lo que la API usa del caso de uso de compras. Lo cumple
// *purchases.Service; los tests usan uno en memoria.
type PurchaseService interface {
	Receive(ctx context.Context, by int64, p *model.Purchase) (*purchases.Result, error)
	Get(ctx context.Context, id int64) (*model.Purchase, error)
}

var _ PurchaseService = (*purchases.Service)(nil)

// PurchasesHandler es la API de la recepción de compras (F-06).
type PurchasesHandler struct {
	svc PurchaseService
}

func NewPurchasesHandler(svc PurchaseService) *PurchasesHandler {
	return &PurchasesHandler{svc: svc}
}

// Register monta los endpoints en base (ej. "/api/purchases"):
//
//	POST {base}       recibir una compra
//	GET  {base}/{id}  una compra con sus renglones
func (h *PurchasesHandler) Register(mux *http.ServeMux, base string) {
	mux.HandleFunc("POST "+base, h.Receive)
	mux.HandleFunc("GET "+base+"/{id}", h.Get)
}

// receiveRequest es el body de POST: solo lo que escribe el usuario. El
// estado, la fecha, el usuario, los lotes y los costos los pone el servidor
// (un campo de más se rechaza).
type receiveRequest struct {
	SupplierID    int64           `json:"supplier_id"`
	VoucherNumber string          `json:"voucher_number"`
	Total         decimal.Decimal `json:"total"` // subtotal sin IVA de la factura
	Items         []struct {
		ProductID   int64           `json:"product_id"`
		Quantity    decimal.Decimal `json:"quantity"`
		UnitCost    decimal.Decimal `json:"unit_cost"` // sin IVA
		BatchNumber string          `json:"batch_number"`
		ExpiryDate  string          `json:"expiry_date"` // "2027-03-31"
	} `json:"items"`
}

// Receive handles POST {base}. Responde 201 con {purchase, total_difference,
// suggestions}. total_difference distinto de "0" es un aviso: la compra se
// guardó igual.
func (h *PurchasesHandler) Receive(w http.ResponseWriter, r *http.Request) {
	var body receiveRequest
	if err := readJSON(r, &body); err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	p := &model.Purchase{SupplierID: body.SupplierID, VoucherNumber: body.VoucherNumber, Total: body.Total}
	for _, it := range body.Items {
		p.Items = append(p.Items, &model.PurchaseItem{ProductID: it.ProductID, Quantity: it.Quantity,
			UnitCost: it.UnitCost, BatchNumber: it.BatchNumber, ExpiryDate: it.ExpiryDate})
	}
	res, err := h.svc.Receive(r.Context(), UserID(r.Context()), p)
	if err != nil {
		purchasesError(w, r, err)
		return
	}
	if err := writeJSON(w, http.StatusCreated, res); err != nil {
		errorJSON(w, http.StatusInternalServerError, "failed to encode response")
	}
}

// Get handles GET {base}/{id}.
func (h *PurchasesHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id", "purchase")
	if !ok {
		return
	}
	p, err := h.svc.Get(r.Context(), id)
	if err != nil {
		purchasesError(w, r, err)
		return
	}
	if err := writeJSON(w, http.StatusOK, p); err != nil {
		errorJSON(w, http.StatusInternalServerError, "failed to encode response")
	}
}

// purchasesError responde los errores del caso de uso. Los de validación y
// conflicto llevan el mensaje entero, que dice qué renglón falló
// ("line 2: product is inactive: conflict").
func purchasesError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		errorJSON(w, http.StatusNotFound, "purchase not found")
	case errors.Is(err, service.ErrInvalidID):
		errorJSON(w, http.StatusBadRequest, "invalid id")
	case errors.Is(err, service.ErrValidation):
		errorJSON(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrConflict):
		errorJSON(w, http.StatusConflict, err.Error())
	default:
		slog.Error("api request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		errorJSON(w, http.StatusInternalServerError, "internal error")
	}
}
