package transport

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/komonte/Project-ElmanPOS/backend/internal/service"
)

type ResourceHandler[T any] struct {
	svc   *service.Service[T]
	label string
}

func NewResourceHandler[T any](s *service.Service[T], label string) *ResourceHandler[T] {
	return &ResourceHandler[T]{
		svc:   s,
		label: label,
	}
}

// DefaultListLimit es cuántas filas devuelve GET {base} si no se pide
// ?limit=. El catálogo tiene decenas de miles de productos: sin un límite,
// un pedido devolvería un JSON de varios MB.
const DefaultListLimit = 100

// List handles GET {base}.
//   - ?name=: búsqueda rápida entre los activos (hasta 20).
//   - Si no: lista paginada de activos e inactivos (cada uno trae is_active),
//     filtrada con ?search= y cortada en ?limit= (DefaultListLimit si falta,
//     tope service.MaxListLimit). El header X-Total-Count dice cuántos pasan
//     el filtro en total.
func (h *ResourceHandler[T]) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var entities []*T
	var err error

	query := strings.TrimSpace(r.URL.Query().Get("name"))
	if query != "" {
		entities, err = h.svc.SearchByName(ctx, query)
		if err != nil {
			errorJSON(w, http.StatusInternalServerError,
				fmt.Sprintf("failed to search %ss", h.label))
			return
		}
	} else {
		limit := DefaultListLimit
		if v := r.URL.Query().Get("limit"); v != "" {
			n, convErr := strconv.Atoi(v)
			if convErr != nil || n < 0 {
				errorJSON(w, http.StatusBadRequest, "invalid limit")
				return
			}
			limit = n
		}
		var total int
		entities, total, err = h.svc.ListAll(ctx, r.URL.Query().Get("search"), limit)
		if err != nil {
			errorJSON(w, http.StatusInternalServerError,
				fmt.Sprintf("failed to retrieve %ss", h.label))
			return
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
	}

	if err := writeJSON(w, http.StatusOK, entities); err != nil {
		errorJSON(w, http.StatusInternalServerError, "failed to encode response")
	}
}

// Create handles POST {base}.
func (h *ResourceHandler[T]) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var entity T

	if err := readJSON(r, &entity); err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}

	created, err := h.svc.Create(ctx, &entity)
	if err != nil {
		h.serviceError(w, r, err)
		return
	}

	if err := writeJSON(w, http.StatusCreated, created); err != nil {
		errorJSON(w, http.StatusInternalServerError, "failed to encode response")
	}
}

// parseID reads the {id} path wildcard. On failure it writes a 400 and returns false.
func (h *ResourceHandler[T]) parseID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		errorJSON(w, http.StatusBadRequest,
			fmt.Sprintf("invalid %s id", h.label))
		return 0, false
	}
	return id, true
}

// Get handles GET {base}/{id}.
func (h *ResourceHandler[T]) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, ok := h.parseID(w, r)
	if !ok {
		return
	}

	entity, err := h.svc.GetByID(ctx, id)
	if err != nil {
		h.serviceError(w, r, err)
		return
	}

	if err := writeJSON(w, http.StatusOK, entity); err != nil {
		errorJSON(w, http.StatusInternalServerError, "failed to encode response")
	}
}

// Update handles PUT {base}/{id}.
func (h *ResourceHandler[T]) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, ok := h.parseID(w, r)
	if !ok {
		return
	}

	var entity T

	if err := readJSON(r, &entity); err != nil {
		errorJSON(w, http.StatusBadRequest, "invalid request body")
		return
	}

	updated, err := h.svc.Update(ctx, id, &entity)
	if err != nil {
		h.serviceError(w, r, err)
		return
	}

	if err := writeJSON(w, http.StatusOK, updated); err != nil {
		errorJSON(w, http.StatusInternalServerError, "failed to encode response")
	}
}

// Delete handles DELETE {base}/{id}.
func (h *ResourceHandler[T]) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, ok := h.parseID(w, r)
	if !ok {
		return
	}

	err := h.svc.Delete(ctx, id)
	if err != nil {
		h.serviceError(w, r, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// serviceError responde el error de una operación sobre un id o de una
// escritura. Los errores conocidos son culpa del pedido (4xx) y su mensaje
// se puede mostrar. Cualquier otro (ej. la DB caída) es 500: se loguea con
// el detalle, pero el cliente solo ve "internal error".
func (h *ResourceHandler[T]) serviceError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, service.ErrNotFound):
		errorJSON(w, http.StatusNotFound, fmt.Sprintf("%s not found", h.label))
	case errors.Is(err, service.ErrInvalidID):
		errorJSON(w, http.StatusBadRequest, fmt.Sprintf("invalid %s id", h.label))
	case errors.Is(err, service.ErrValidation),
		errors.Is(err, service.ErrInvalidReference):
		// El mensaje dice qué está mal, ej. "validation failed: category tax is required".
		errorJSON(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrAlreadyExists):
		errorJSON(w, http.StatusConflict, fmt.Sprintf("%s already exists", h.label))
	case errors.Is(err, service.ErrInUse):
		errorJSON(w, http.StatusConflict, fmt.Sprintf("%s is in use", h.label))
	case errors.Is(err, service.ErrConflict):
		// ej. "conflict: category has active subcategories"
		errorJSON(w, http.StatusConflict, err.Error())
	default:
		slog.Error("api request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		errorJSON(w, http.StatusInternalServerError, "internal error")
	}
}
