package transport

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/prices"
)

// PriceService es lo que la API usa del caso de uso de precios. Lo cumple
// *prices.Service; los tests usan uno en memoria.
type PriceService interface {
	Set(ctx context.Context, productID int64, unitPrice decimal.Decimal) (*model.Price, error)
	History(ctx context.Context, productID int64) ([]*model.Price, error)
}

var _ PriceService = (*prices.Service)(nil)

// PricesHandler es la API del precio de venta de un producto (F-03). Cuelga
// del producto: /api/products/{id}/prices.
type PricesHandler struct {
	svc PriceService
}

func NewPricesHandler(svc PriceService) *PricesHandler {
	return &PricesHandler{svc: svc}
}

// Register monta los endpoints debajo de base (ej. "/api/products"):
//
//	GET  {base}/{id}/prices  el precio vigente y la historia
//	POST {base}/{id}/prices  un precio nuevo, vigente desde ya: {"unit_price": "1500.50"}
func (h *PricesHandler) Register(mux *http.ServeMux, base string) {
	mux.HandleFunc("GET "+base+"/{id}/prices", h.List)
	mux.HandleFunc("POST "+base+"/{id}/prices", h.Set)
}

// pricesResponse: current es el vigente (null si el producto todavía no tiene
// precio) y history todos, del más nuevo al más viejo.
type pricesResponse struct {
	Current *model.Price   `json:"current"`
	History []*model.Price `json:"history"`
}

// List handles GET {base}/{id}/prices.
func (h *PricesHandler) List(w http.ResponseWriter, r *http.Request) {
	productID, ok := pathID(w, r, "id", "product")
	if !ok {
		return
	}
	history, err := h.svc.History(r.Context(), productID)
	if err != nil {
		pricesError(w, r, err)
		return
	}
	resp := pricesResponse{History: history}
	if len(history) > 0 {
		resp.Current = history[0]
	}
	if err := writeJSON(w, http.StatusOK, resp); err != nil {
		errorJSON(w, http.StatusInternalServerError, "failed to encode response")
	}
}

// Set handles POST {base}/{id}/prices. unit_price puede venir como texto
// ("1500.50") o como número (1500.50); el texto evita redondeos del cliente.
// La fecha la pone el servidor: el body solo trae el precio.
func (h *PricesHandler) Set(w http.ResponseWriter, r *http.Request) {
	productID, ok := pathID(w, r, "id", "product")
	if !ok {
		return
	}
	var body struct {
		UnitPrice decimal.Decimal `json:"unit_price"`
	}
	if err := readJSON(r, &body); err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	saved, err := h.svc.Set(r.Context(), productID, body.UnitPrice)
	if err != nil {
		pricesError(w, r, err)
		return
	}
	if err := writeJSON(w, http.StatusCreated, saved); err != nil {
		errorJSON(w, http.StatusInternalServerError, "failed to encode response")
	}
}

// pricesError responde los errores del caso de uso, con los mismos códigos
// HTTP que codesError.
func pricesError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		errorJSON(w, http.StatusNotFound, "product not found")
	case errors.Is(err, service.ErrInvalidID):
		errorJSON(w, http.StatusBadRequest, "invalid id")
	case errors.Is(err, service.ErrValidation):
		// ej. "validation failed: price must be greater than 0, ..."
		errorJSON(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrConflict):
		// ej. "product is inactive: conflict"
		errorJSON(w, http.StatusConflict, err.Error())
	default:
		slog.Error("api request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		errorJSON(w, http.StatusInternalServerError, "internal error")
	}
}
