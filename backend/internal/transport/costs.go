package transport

import (
	"context"
	"net/http"

	"github.com/shopspring/decimal"

	"github.com/komonte/Project-ElmanPOS/backend/internal/model"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/costs"
)

// CostService es lo que la API usa del caso de uso de costos. Lo cumple
// *costs.Service; los tests usan uno en memoria.
type CostService interface {
	Set(ctx context.Context, by, productID int64, unitCost decimal.Decimal) (*costs.Result, error)
	History(ctx context.Context, productID int64) ([]*model.Cost, error)
}

var _ CostService = (*costs.Service)(nil)

// CostsHandler es la API del costo de un producto (F-04, carga manual).
// Cuelga del producto: /api/products/{id}/costs.
type CostsHandler struct {
	svc CostService
}

func NewCostsHandler(svc CostService) *CostsHandler {
	return &CostsHandler{svc: svc}
}

// Register monta los endpoints debajo de base (ej. "/api/products"):
//
//	GET  {base}/{id}/costs  el costo vigente y la historia
//	POST {base}/{id}/costs  un costo nuevo, vigente desde ya: {"unit_cost": "700"}
func (h *CostsHandler) Register(mux *http.ServeMux, base string) {
	mux.HandleFunc("GET "+base+"/{id}/costs", h.List)
	mux.HandleFunc("POST "+base+"/{id}/costs", h.Set)
}

// costsResponse: current es el vigente (null si el producto todavía no tiene
// costo) y history todos, del más nuevo al más viejo.
type costsResponse struct {
	Current *model.Cost   `json:"current"`
	History []*model.Cost `json:"history"`
}

// List handles GET {base}/{id}/costs.
func (h *CostsHandler) List(w http.ResponseWriter, r *http.Request) {
	productID, ok := pathID(w, r, "id", "product")
	if !ok {
		return
	}
	history, err := h.svc.History(r.Context(), productID)
	if err != nil {
		pricesError(w, r, err) // mismos errores y códigos que los precios
		return
	}
	resp := costsResponse{History: history}
	if len(history) > 0 {
		resp.Current = history[0]
	}
	if err := writeJSON(w, http.StatusOK, resp); err != nil {
		errorJSON(w, http.StatusInternalServerError, "failed to encode response")
	}
}

// Set handles POST {base}/{id}/costs. unit_cost puede venir como texto ("700")
// o como número (700). El body solo trae el costo: el promedio lo calcula el
// servidor y la fecha también la pone él.
//
// Responde 201 con {"cost": {...}, "suggested_price": "1000"};
// suggested_price es null si el producto tiene margen 0. La sugerencia no se
// guarda: para usarla, el cliente hace POST a /prices.
func (h *CostsHandler) Set(w http.ResponseWriter, r *http.Request) {
	productID, ok := pathID(w, r, "id", "product")
	if !ok {
		return
	}
	var body struct {
		UnitCost decimal.Decimal `json:"unit_cost"`
	}
	if err := readJSON(r, &body); err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	res, err := h.svc.Set(r.Context(), UserID(r.Context()), productID, body.UnitCost)
	if err != nil {
		pricesError(w, r, err)
		return
	}
	if err := writeJSON(w, http.StatusCreated, res); err != nil {
		errorJSON(w, http.StatusInternalServerError, "failed to encode response")
	}
}
