package transport

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
	"github.com/komonte/Project-ElmanPOS/backend/internal/usecase/productcodes"
)

// ProductCodesHandler es la API de los códigos de barra de un producto
// (F-02). Cuelga del producto: /api/products/{id}/codes.
type ProductCodesHandler struct {
	svc *productcodes.Service
}

func NewProductCodesHandler(svc *productcodes.Service) *ProductCodesHandler {
	return &ProductCodesHandler{svc: svc}
}

// Register monta los endpoints debajo de base (ej. "/api/products"):
//
//	GET    {base}/{id}/codes           lista los códigos del producto
//	POST   {base}/{id}/codes           agrega uno: {"code": "7790070991232"}
//	DELETE {base}/{id}/codes/{codeID}  quita uno
func (h *ProductCodesHandler) Register(mux *http.ServeMux, base string) {
	mux.HandleFunc("GET "+base+"/{id}/codes", h.List)
	mux.HandleFunc("POST "+base+"/{id}/codes", h.Add)
	mux.HandleFunc("DELETE "+base+"/{id}/codes/{codeID}", h.Remove)
}

// pathID lee un comodín numérico de la ruta. Si no es un número responde 400
// y devuelve false.
func pathID(w http.ResponseWriter, r *http.Request, name, label string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid "+label+" id")
		return 0, false
	}
	return id, true
}

// List handles GET {base}/{id}/codes.
func (h *ProductCodesHandler) List(w http.ResponseWriter, r *http.Request) {
	productID, ok := pathID(w, r, "id", "product")
	if !ok {
		return
	}
	codes, err := h.svc.List(r.Context(), productID)
	if err != nil {
		codesError(w, r, err, "product not found")
		return
	}
	if err := writeJSON(w, http.StatusOK, codes); err != nil {
		errorJSON(w, http.StatusInternalServerError, "failed to encode response")
	}
}

// Add handles POST {base}/{id}/codes. El código se normaliza y el tipo lo
// calcula el servidor: el body solo trae "code".
func (h *ProductCodesHandler) Add(w http.ResponseWriter, r *http.Request) {
	productID, ok := pathID(w, r, "id", "product")
	if !ok {
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := readJSON(r, &body); err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}
	added, err := h.svc.Add(r.Context(), productID, body.Code)
	if err != nil {
		codesError(w, r, err, "product not found")
		return
	}
	if err := writeJSON(w, http.StatusCreated, added); err != nil {
		errorJSON(w, http.StatusInternalServerError, "failed to encode response")
	}
}

// Remove handles DELETE {base}/{id}/codes/{codeID}.
func (h *ProductCodesHandler) Remove(w http.ResponseWriter, r *http.Request) {
	productID, ok := pathID(w, r, "id", "product")
	if !ok {
		return
	}
	codeID, ok := pathID(w, r, "codeID", "barcode")
	if !ok {
		return
	}
	if err := h.svc.Remove(r.Context(), productID, codeID); err != nil {
		// No existe, o es de otro producto: para quien llama es lo mismo.
		codesError(w, r, err, "barcode not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// codesError responde los errores del caso de uso, con los mismos códigos
// HTTP que serviceError. notFound es el mensaje del 404 (qué no se encontró).
func codesError(w http.ResponseWriter, r *http.Request, err error, notFound string) {
	var taken *productcodes.CodeTakenError
	switch {
	case errors.As(err, &taken):
		// ej. "barcode 7790070991232 already belongs to product 000123 COCA COLA 2L"
		errorJSON(w, http.StatusConflict, taken.Error())
	case errors.Is(err, service.ErrAlreadyExists):
		errorJSON(w, http.StatusConflict, "barcode already exists")
	case errors.Is(err, service.ErrNotFound):
		errorJSON(w, http.StatusNotFound, notFound)
	case errors.Is(err, service.ErrInvalidID):
		errorJSON(w, http.StatusBadRequest, "invalid id")
	case errors.Is(err, service.ErrValidation):
		// ej. "validation failed: barcode can only contain digits"
		errorJSON(w, http.StatusBadRequest, err.Error())
	default:
		slog.Error("api request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		errorJSON(w, http.StatusInternalServerError, "internal error")
	}
}
